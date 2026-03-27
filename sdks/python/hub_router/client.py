"""Async clients for hub-router — fully non-blocking, asyncio-native."""
from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable, Optional

import httpx

from .exceptions import (
    HubRouterError,
    QueueFullError,
    RequestNotFoundError,
    ResultTimeoutError,
)
from .models import QueuedRequest, Result, SubmitResponse

# Type alias for the processor callback
ProcessorFunc = Callable[[QueuedRequest], Awaitable[Result]]


class OnlineClient:
    """
    Async client for the online (web app) side of hub-router.

    Usage::

        async with OnlineClient("https://hub.example.com", api_key="secret") as client:
            result = await client.do({"query": "hello"})
            print(result.payload)

    Compatible with any asyncio framework (FastAPI, Starlette, Django async, etc.).
    All methods are non-blocking — they never block the event loop.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        long_poll_timeout: float = 30.0,
        max_retries: int = 10,
        http_client: Optional[httpx.AsyncClient] = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._long_poll_timeout = long_poll_timeout
        self._max_retries = max_retries
        self._owned_client = http_client is None
        self._client = http_client or httpx.AsyncClient(
            timeout=httpx.Timeout(long_poll_timeout + 10)
        )

    async def __aenter__(self) -> "OnlineClient":
        return self

    async def __aexit__(self, *_: Any) -> None:
        if self._owned_client:
            await self._client.aclose()

    async def submit(
        self,
        payload: Any,
        headers: Optional[dict[str, str]] = None,
    ) -> str:
        """
        Enqueue a request and return its correlation ID.

        :param payload: Any JSON-serialisable value.
        :param headers: Optional metadata forwarded with the request.
        :returns: The request ID (use with :meth:`wait_result`).
        :raises QueueFullError: If the middleware queue is at capacity.
        """
        resp = await self._client.post(
            f"{self._base_url}/request",
            json={"payload": payload, "headers": headers or {}},
            headers={"X-Online-API-Key": self._api_key},
        )
        if resp.status_code == 503:
            data = resp.json()
            raise QueueFullError(data.get("message", "queue full"))
        if resp.status_code != 202:
            raise HubRouterError(f"unexpected status {resp.status_code}: {resp.text}")

        data = resp.json()
        return data["id"]

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
        :returns: The :class:`Result` from the local server.
        :raises RequestNotFoundError: If the ID is unknown or expired.
        :raises ResultTimeoutError: If ``max_retries`` 204 responses are received.
        """
        t = timeout or self._long_poll_timeout
        url = f"{self._base_url}/result/{request_id}?timeout={t}s"
        hdrs = {"X-Online-API-Key": self._api_key}

        for attempt in range(self._max_retries + 1):
            resp = await self._client.get(url, headers=hdrs)

            if resp.status_code == 200:
                return Result.from_dict(resp.json())

            if resp.status_code == 204:
                # Server long-poll timed out — retry immediately (same ID).
                continue

            if resp.status_code == 404:
                raise RequestNotFoundError(
                    f"request ID {request_id!r} not found (may have expired)"
                )

            raise HubRouterError(
                f"unexpected status {resp.status_code}: {resp.text}"
            )

        raise ResultTimeoutError(
            f"result not available after {self._max_retries} retries "
            f"for request {request_id!r}"
        )

    async def do(
        self,
        payload: Any,
        headers: Optional[dict[str, str]] = None,
    ) -> Result:
        """
        Convenience wrapper: :meth:`submit` + :meth:`wait_result` in one call.
        """
        request_id = await self.submit(payload, headers)
        return await self.wait_result(request_id)


class LocalClient:
    """
    Async client for the local processing server side of hub-router.

    Usage::

        async def process(req: QueuedRequest) -> Result:
            answer = await my_model.infer(req.payload)
            return Result(request_id=req.id, payload=answer)

        async with LocalClient("https://hub.example.com", api_key="local-secret",
                               batch_size=20, workers=5) as client:
            # Run inside an asyncio event loop alongside other tasks:
            task = asyncio.create_task(client.run(process))
            # ... or simply await if this is the main loop work:
            await client.run(process)

    The poll loop never blocks the event loop:
    - Uses ``asyncio.sleep`` when the queue is empty.
    - Dispatches each request as an ``asyncio.Task``, bounded by an
      ``asyncio.Semaphore(workers)`` to prevent goroutine explosion.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        batch_size: int = 10,
        poll_interval: float = 1.0,
        workers: int = 1,
        http_client: Optional[httpx.AsyncClient] = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._workers = workers
        self._owned_client = http_client is None
        self._client = http_client or httpx.AsyncClient(timeout=httpx.Timeout(30.0))
        self._stop_event = asyncio.Event()

    async def __aenter__(self) -> "LocalClient":
        return self

    async def __aexit__(self, *_: Any) -> None:
        self._stop_event.set()
        if self._owned_client:
            await self._client.aclose()

    async def run(self, processor: ProcessorFunc) -> None:
        """
        Start the poll loop. Blocks until the client is closed or the task is cancelled.

        Dispatches each request as a separate asyncio task (bounded by ``workers`` semaphore).
        Results are pushed back to hub-router automatically.
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
                    break  # stop event fired
                except asyncio.TimeoutError:
                    continue

            for req in requests:
                task = asyncio.create_task(
                    self._process_and_push(sem, processor, req)
                )
                pending_tasks.add(task)
                task.add_done_callback(pending_tasks.discard)

        # Drain in-flight tasks before returning.
        if pending_tasks:
            await asyncio.gather(*pending_tasks, return_exceptions=True)

    async def pull_batch(self) -> list[QueuedRequest]:
        """Fetch up to ``batch_size`` pending requests. Never blocks."""
        resp = await self._client.get(
            f"{self._base_url}/queue/pull",
            params={"batch": self._batch_size},
            headers={"X-Local-API-Key": self._api_key},
        )
        if resp.status_code != 200:
            raise HubRouterError(
                f"unexpected status {resp.status_code}: {resp.text}"
            )
        data = resp.json()
        return [QueuedRequest.from_dict(r) for r in data.get("requests", [])]

    async def push_result(self, result: Result) -> None:
        """Push a completed result to hub-router."""
        if result.completed_at is None:
            result.completed_at = datetime.now(timezone.utc)

        resp = await self._client.post(
            f"{self._base_url}/queue/result",
            json=result.to_dict(),
            headers={"X-Local-API-Key": self._api_key},
        )
        if resp.status_code == 404:
            raise RequestNotFoundError(
                f"request ID {result.request_id!r} not found (may have expired)"
            )
        if resp.status_code != 204:
            raise HubRouterError(
                f"unexpected status {resp.status_code}: {resp.text}"
            )

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
