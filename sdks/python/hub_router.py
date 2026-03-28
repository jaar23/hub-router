"""
hub-router SDK — single-file Python client.

Copy this file into your project. No pip install required. Requires Python 3.9+.

Usage::

    from hub_router import OnlineClient, LocalClient

    # Online side — submit and wait for results
    client = OnlineClient("https://hub.example.com", api_key="secret")
    result = await client.do({"query": "hello"})

    # Forward an incoming FastAPI/Starlette request verbatim
    result = await client.do_request(request)

    # Local side — process requests from the queue
    local = LocalClient("https://hub.example.com", api_key="local-secret")
    async def process(req):
        return Result(request_id=req.id, payload=await my_model(req.payload))
    await local.run(process)
"""
from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable, Optional

# ─── Exceptions ───────────────────────────────────────────────────────────────


class HubRouterError(Exception):
    """Base exception for all hub-router errors."""


class QueueFullError(HubRouterError):
    """Raised when the middleware queue is at capacity."""


class RequestNotFoundError(HubRouterError):
    """Raised when a request ID is not found (expired or never existed)."""


class ResultTimeoutError(HubRouterError):
    """Raised when wait_result exhausts all retries without receiving a result."""


# ─── Models ───────────────────────────────────────────────────────────────────


@dataclass
class QueuedRequest:
    """A pending request pulled by the local server."""

    id: str
    payload: Any
    headers: dict[str, str] = field(default_factory=dict)
    enqueued_at: Optional[datetime] = None
    expires_at: Optional[datetime] = None

    @classmethod
    def from_dict(cls, data: dict) -> "QueuedRequest":
        return cls(
            id=data["id"],
            payload=data["payload"],
            headers=data.get("headers") or {},
            enqueued_at=_parse_dt(data.get("enqueued_at")),
            expires_at=_parse_dt(data.get("expires_at")),
        )


@dataclass
class Result:
    """A processed result pushed back by the local server."""

    request_id: str
    payload: Any = None
    status_code: int = 200
    error: str = ""
    completed_at: Optional[datetime] = None

    def to_dict(self) -> dict:
        return {
            "request_id": self.request_id,
            "payload": self.payload,
            "status_code": self.status_code,
            "error": self.error,
            "completed_at": self.completed_at.isoformat() if self.completed_at else None,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Result":
        return cls(
            request_id=data["request_id"],
            payload=data.get("payload"),
            status_code=data.get("status_code", 200),
            error=data.get("error", ""),
            completed_at=_parse_dt(data.get("completed_at")),
        )


def _parse_dt(value: Optional[str]) -> Optional[datetime]:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except (ValueError, AttributeError):
        return None


# ─── HTTP helper (stdlib, no external deps) ───────────────────────────────────

_HOP_BY_HOP = frozenset(
    {
        "content-length",
        "host",
        "transfer-encoding",
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailers",
        "upgrade",
    }
)


async def _http(
    method: str,
    url: str,
    *,
    data: Optional[bytes] = None,
    headers: Optional[dict[str, str]] = None,
) -> tuple[int, bytes]:
    """Non-blocking HTTP request using stdlib urllib wrapped in a thread."""

    def _blocking() -> tuple[int, bytes]:
        req = urllib.request.Request(
            url,
            data=data,
            headers=headers or {},
            method=method,
        )
        try:
            with urllib.request.urlopen(req) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read()

    return await asyncio.to_thread(_blocking)


# ─── Type alias ──────────────────────────────────────────────────────────────

ProcessorFunc = Callable[["QueuedRequest"], Awaitable["Result"]]


# ─── OnlineClient ─────────────────────────────────────────────────────────────


class OnlineClient:
    """
    Async client for the online (web app) side of hub-router.

    All methods are non-blocking — they never block the event loop.
    Compatible with FastAPI, Starlette, Django async, or any asyncio app.

    Usage::

        client = OnlineClient("https://hub.example.com", api_key="secret")

        # Submit a payload and wait for the result
        result = await client.do({"query": "hello"})
        print(result.payload)

        # Forward an incoming request verbatim (FastAPI example)
        @app.post("/infer")
        async def infer(request: Request):
            result = await client.do_request(request)
            return result.payload
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        long_poll_timeout: float = 30.0,
        max_retries: int = 10,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._long_poll_timeout = long_poll_timeout
        self._max_retries = max_retries

    async def submit(
        self,
        payload: Any,
        headers: Optional[dict[str, str]] = None,
    ) -> str:
        """
        Enqueue a request and return its correlation ID.

        :raises QueueFullError: If the middleware queue is at capacity.
        """
        body = json.dumps({"payload": payload, "headers": headers or {}}).encode()
        status, raw = await _http(
            "POST",
            f"{self._base_url}/request",
            data=body,
            headers={
                "Content-Type": "application/json",
                "X-Online-API-Key": self._api_key,
            },
        )
        if status == 503:
            raise QueueFullError(json.loads(raw).get("message", "queue full"))
        if status != 202:
            raise HubRouterError(f"unexpected status {status}: {raw.decode()}")
        return json.loads(raw)["id"]

    async def wait_result(
        self,
        request_id: str,
        *,
        timeout: Optional[float] = None,
    ) -> Result:
        """
        Long-poll until the result for *request_id* is ready.

        :param request_id: The ID returned by :meth:`submit`.
        :param timeout: Per-poll timeout in seconds (defaults to ``long_poll_timeout``).
        :raises RequestNotFoundError: If the ID is unknown or expired.
        :raises ResultTimeoutError: If ``max_retries`` 204 responses are received.
        """
        t = timeout or self._long_poll_timeout
        url = f"{self._base_url}/result/{request_id}?timeout={t}s"
        hdrs = {"X-Online-API-Key": self._api_key}

        for _ in range(self._max_retries + 1):
            status, raw = await _http("GET", url, headers=hdrs)
            if status == 200:
                return Result.from_dict(json.loads(raw))
            if status == 204:
                continue
            if status == 404:
                raise RequestNotFoundError(
                    f"request ID {request_id!r} not found (may have expired)"
                )
            raise HubRouterError(f"unexpected status {status}: {raw.decode()}")

        raise ResultTimeoutError(
            f"result not available after {self._max_retries} retries "
            f"for request {request_id!r}"
        )

    async def do_sync(
        self,
        payload: Any,
        headers: Optional[dict[str, str]] = None,
        *,
        timeout: Optional[float] = None,
    ) -> tuple[Optional[Result], Optional[str]]:
        """
        Single-request fast path using POST /request/sync.

        Returns ``(Result, None)`` if the local server responds before the timeout,
        or ``(None, request_id)`` on slow path — continue with :meth:`wait_result`.

        :raises QueueFullError: If the queue is at capacity.
        """
        t = timeout or self._long_poll_timeout
        body = json.dumps({"payload": payload, "headers": headers or {}}).encode()
        status, raw = await _http(
            "POST",
            f"{self._base_url}/request/sync?timeout={t}s",
            data=body,
            headers={
                "Content-Type": "application/json",
                "X-Online-API-Key": self._api_key,
            },
        )
        if status == 503:
            raise QueueFullError(json.loads(raw).get("message", "queue full"))
        if status == 200:
            return Result.from_dict(json.loads(raw)), None
        if status == 202:
            return None, json.loads(raw)["id"]
        raise HubRouterError(f"unexpected status {status}: {raw.decode()}")

    async def do(
        self,
        payload: Any,
        headers: Optional[dict[str, str]] = None,
    ) -> Result:
        """
        Submit a request and wait for its result.

        Tries the sync fast path first (single round-trip when local server is fast).
        Falls back to async polling transparently when local server is slow.
        """
        result, request_id = await self.do_sync(payload, headers)
        if result is not None:
            return result
        return await self.wait_result(request_id)  # type: ignore[arg-type]

    async def do_request(
        self,
        request: Any,
        *,
        timeout: Optional[float] = None,
    ) -> Result:
        """
        Forward an incoming HTTP request verbatim to hub-router.

        The original headers and body are passed through to the local server
        unchanged. Hop-by-hop headers (Content-Length, Host, etc.) are excluded.

        Accepts any request object with:
        - ``.headers`` — dict-like mapping of header name → value
        - ``.body()`` — async method (or ``.body`` attribute) returning bytes

        Compatible with FastAPI and Starlette ``Request`` objects::

            @app.post("/infer")
            async def infer(request: Request):
                result = await client.do_request(request)
                return result.payload
        """
        # Extract headers, skipping hop-by-hop
        fwd_headers: dict[str, str] = {}
        raw_headers = getattr(request, "headers", {})
        items = raw_headers.items() if hasattr(raw_headers, "items") else raw_headers
        for k, v in items:
            if k.lower() not in _HOP_BY_HOP:
                fwd_headers[k] = v

        # Read body — support async .body() coroutine or sync .body attribute
        body_attr = getattr(request, "body", None)
        if callable(body_attr):
            raw_body = await body_attr()
        elif body_attr is not None:
            raw_body = body_attr
        else:
            raw_body = b""

        payload = (
            raw_body.decode("utf-8", errors="replace")
            if isinstance(raw_body, bytes)
            else str(raw_body or "")
        )
        return await self.do(payload, fwd_headers)


# ─── LocalClient ─────────────────────────────────────────────────────────────


class LocalClient:
    """
    Async client for the local processing server side of hub-router.

    All methods are non-blocking — they never block the event loop.

    Usage::

        async def process(req: QueuedRequest) -> Result:
            answer = await my_model.infer(req.payload)
            return Result(request_id=req.id, payload=answer)

        client = LocalClient("https://hub.example.com", api_key="local-secret",
                             batch_size=20, workers=5)
        # Run alongside other async tasks:
        task = asyncio.create_task(client.run(process))
        # Or block:
        await client.run(process)
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        batch_size: int = 10,
        poll_interval: float = 1.0,
        workers: int = 1,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._workers = workers
        self._stop_event = asyncio.Event()

    def stop(self) -> None:
        """Signal the poll loop to stop after the current batch finishes."""
        self._stop_event.set()

    async def run(self, processor: ProcessorFunc) -> None:
        """
        Start the poll loop. Blocks until :meth:`stop` is called or task is cancelled.

        Dispatches each request as a separate asyncio task, bounded by the
        ``workers`` semaphore to prevent concurrency explosion.
        """
        sem = asyncio.Semaphore(self._workers)
        pending_tasks: set[asyncio.Task] = set()

        while not self._stop_event.is_set():
            requests = await self.pull_batch()

            if not requests:
                try:
                    await asyncio.wait_for(
                        asyncio.shield(self._stop_event.wait()),
                        timeout=self._poll_interval,
                    )
                    break
                except asyncio.TimeoutError:
                    continue

            for req in requests:
                task = asyncio.create_task(
                    self._process_and_push(sem, processor, req)
                )
                pending_tasks.add(task)
                task.add_done_callback(pending_tasks.discard)

        if pending_tasks:
            await asyncio.gather(*pending_tasks, return_exceptions=True)

    async def pull_batch(self) -> list[QueuedRequest]:
        """Fetch up to ``batch_size`` pending requests. Never blocks."""
        status, raw = await _http(
            "GET",
            f"{self._base_url}/queue/pull?batch={self._batch_size}",
            headers={"X-Local-API-Key": self._api_key},
        )
        if status != 200:
            raise HubRouterError(f"unexpected status {status}: {raw.decode()}")
        data = json.loads(raw)
        return [QueuedRequest.from_dict(r) for r in data.get("requests", [])]

    async def push_result(self, result: Result) -> None:
        """Push a completed result to hub-router."""
        if result.completed_at is None:
            result.completed_at = datetime.now(timezone.utc)
        body = json.dumps(result.to_dict()).encode()
        status, raw = await _http(
            "POST",
            f"{self._base_url}/queue/result",
            data=body,
            headers={
                "Content-Type": "application/json",
                "X-Local-API-Key": self._api_key,
            },
        )
        if status == 404:
            raise RequestNotFoundError(
                f"request ID {result.request_id!r} not found (may have expired)"
            )
        if status != 204:
            raise HubRouterError(f"unexpected status {status}: {raw.decode()}")

    async def _process_and_push(
        self,
        sem: asyncio.Semaphore,
        processor: ProcessorFunc,
        req: QueuedRequest,
    ) -> None:
        async with sem:
            try:
                result = await processor(req)
            except Exception as exc:  # noqa: BLE001
                result = Result(
                    request_id=req.id,
                    payload={"error": str(exc)},
                    status_code=500,
                    error=str(exc),
                )
            try:
                await self.push_result(result)
            except HubRouterError:
                pass  # best-effort; request may have expired
