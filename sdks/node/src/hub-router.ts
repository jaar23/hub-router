/**
 * hub-router SDK — single-file TypeScript client.
 *
 * Copy this file into your project. No npm install required.
 * Requires Node.js 18+ (native fetch) or any environment with the Web Fetch API.
 *
 * Usage:
 *   import { OnlineClient, LocalClient } from "./hub-router.js";
 *
 *   const client = new OnlineClient("https://hub.example.com", "api-key");
 *
 *   // Simple payload (routes to "default" queue)
 *   const result = await client.do({ query: "hello" });
 *
 *   // Route to a named queue
 *   const result = await client.do({ query: "hello" }, {}, { key: "gpu" });
 *
 *   // Check the result
 *   if (result.isError()) throw result.err();
 *   const data = result.unmarshal<MyType>();
 *
 *   // Forward an incoming HTTP request verbatim:
 *   const result = await client.doRequest(req, { key: "gpu" });
 *
 *   // Local side — pull only from the "gpu" queue
 *   const local = new LocalClient("https://hub.example.com", "local-key", { key: "gpu" });
 *   local.run(async (req) => {
 *     const answer = await myModel.infer(req.payload);
 *     return { request_id: req.id, payload: answer, status_code: 200 };
 *   });
 */

// ─── Types ────────────────────────────────────────────────────────────────────

/** A pending request pulled by the local server. */
export interface QueuedRequest {
  id: string;
  key?: string;
  payload: unknown;
  headers: Record<string, string>;
  enqueued_at: string;
  expires_at: string;
}

/** A processed result pushed back by the local server. */
export interface ResultData {
  request_id: string;
  payload: unknown;
  status_code: number;
  error?: string;
  completed_at?: string;
}

/** Response from POST /request or POST /request/sync (202). */
export interface SubmitResponse {
  id: string;
  estimated_wait_ms: number;
}

/** Response from GET /queue/pull. */
export interface PullResponse {
  requests: QueuedRequest[];
  count: number;
}

/** Options for OnlineClient. */
export interface OnlineClientOptions {
  /** Long-poll timeout in milliseconds (default: 30_000). */
  longPollTimeout?: number;
  /** Max number of 204 retries before giving up (default: 10). */
  maxRetries?: number;
}

/** Options for LocalClient. */
export interface LocalClientOptions {
  /** Queue key this worker pulls from (empty → "default"). */
  key?: string;
  /** Requests per poll cycle (default: 10). */
  batchSize?: number;
  /** Sleep in ms when queue is empty (default: 1000). */
  pollInterval?: number;
  /** Max concurrent processor calls (default: 1). */
  workers?: number;
}

/** Per-request options for OnlineClient methods. */
export interface RequestOptions {
  /** Route to a named queue (empty → "default"). */
  key?: string;
  /** Per-request long-poll timeout override in milliseconds. */
  timeout?: number;
}

/** Processor callback: receives a request, returns a result. */
export type ProcessorFn = (req: QueuedRequest) => Promise<ResultData>;

// ─── Result wrapper ───────────────────────────────────────────────────────────

/**
 * Result wraps the server response and provides helper methods.
 *
 * ```ts
 * const result = await client.do({ query: "hello" });
 * if (result.isError()) throw result.err();
 * const data = result.unmarshal<{ answer: string }>();
 * ```
 */
export class Result {
  readonly request_id: string;
  readonly payload: unknown;
  readonly status_code: number;
  readonly error?: string;
  readonly completed_at?: string;

  constructor(data: ResultData) {
    this.request_id = data.request_id;
    this.payload = data.payload;
    this.status_code = data.status_code;
    this.error = data.error;
    this.completed_at = data.completed_at;
  }

  /** Returns true if the result indicates a failure (status >= 400 or non-empty error). */
  isError(): boolean {
    return this.status_code >= 400 || !!this.error;
  }

  /** Returns an Error if isError() is true, else null. */
  err(): Error | null {
    if (!this.isError()) return null;
    const msg = this.error ?? "non-success status";
    return new HubRouterError(`hub-router: ${msg} (status ${this.status_code})`);
  }

  /**
   * Cast the payload to the given type.
   * The payload is already parsed from JSON — no additional deserialization occurs.
   */
  unmarshal<T>(): T {
    return this.payload as T;
  }
}

// ─── Errors ──────────────────────────────────────────────────────────────────

export class HubRouterError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HubRouterError";
  }
}

export class QueueFullError extends HubRouterError {
  constructor(message = "queue is full") {
    super(message);
    this.name = "QueueFullError";
  }
}

export class RequestNotFoundError extends HubRouterError {
  constructor(requestId: string) {
    super(`request ID '${requestId}' not found (may have expired)`);
    this.name = "RequestNotFoundError";
  }
}

export class ResultTimeoutError extends HubRouterError {
  constructor(requestId: string, retries: number) {
    super(
      `result not available after ${retries} retries for request '${requestId}'`
    );
    this.name = "ResultTimeoutError";
  }
}

// ─── OnlineClient ─────────────────────────────────────────────────────────────

const HOP_BY_HOP = new Set([
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
]);

const DEFAULT_LONG_POLL_TIMEOUT_MS = 30_000;
const DEFAULT_MAX_RETRIES = 10;

/**
 * OnlineClient — submit requests and wait for results from hub-router.
 * Fully async, non-blocking. Works with Express, Fastify, NestJS, etc.
 *
 * ```ts
 * const client = new OnlineClient("https://hub.example.com", "api-key");
 *
 * // Simple payload (default queue)
 * const result = await client.do({ query: "hello" });
 *
 * // Route to a named queue
 * const result = await client.do({ query: "hello" }, {}, { key: "gpu" });
 *
 * // Forward an incoming HTTP request verbatim
 * app.post("/api/infer", async (req, res) => {
 *   const result = await client.doRequest(webReq, { key: "gpu" });
 *   res.json(result.payload);
 * });
 * ```
 */
export class OnlineClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly longPollTimeoutMs: number;
  private readonly maxRetries: number;

  /**
   * @param baseUrl  hub-router base URL, e.g. "https://hub.example.com"
   * @param apiKey   X-Online-API-Key value
   * @param options  Optional overrides
   */
  constructor(
    baseUrl: string,
    apiKey: string,
    options: OnlineClientOptions = {}
  ) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
    this.apiKey = apiKey;
    this.longPollTimeoutMs =
      options.longPollTimeout ?? DEFAULT_LONG_POLL_TIMEOUT_MS;
    this.maxRetries = options.maxRetries ?? DEFAULT_MAX_RETRIES;
  }

  /**
   * Enqueue a request and return its correlation ID.
   * @param options.key  Route to a named queue (empty → "default").
   * @throws {QueueFullError} if the queue is at capacity.
   */
  async submit(
    payload: unknown,
    headers: Record<string, string> = {},
    options: RequestOptions = {}
  ): Promise<string> {
    const body: Record<string, unknown> = { payload, headers };
    if (options.key) body["key"] = options.key;

    const res = await fetch(`${this.baseUrl}/request`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Online-API-Key": this.apiKey,
      },
      body: JSON.stringify(body),
    });

    if (res.status === 503) {
      const data = (await res.json()) as { message?: string };
      throw new QueueFullError(data.message);
    }
    if (res.status !== 202) {
      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }

    const data = (await res.json()) as SubmitResponse;
    return data.id;
  }

  /**
   * Long-poll until the result for *requestId* is ready.
   * Retries automatically on 204 (server timeout) up to `maxRetries` times.
   *
   * @throws {RequestNotFoundError} if the ID is unknown or expired.
   * @throws {ResultTimeoutError}   if `maxRetries` exhausted.
   */
  async waitResult(
    requestId: string,
    options?: { timeout?: number }
  ): Promise<Result> {
    const timeoutMs = options?.timeout ?? this.longPollTimeoutMs;
    const timeoutSec = (timeoutMs / 1000).toFixed(1);
    const url = `${this.baseUrl}/result/${encodeURIComponent(requestId)}?timeout=${timeoutSec}s`;

    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      const res = await fetch(url, {
        headers: { "X-Online-API-Key": this.apiKey },
        signal: AbortSignal.timeout(timeoutMs + 10_000),
      });

      if (res.status === 200) return new Result((await res.json()) as ResultData);
      if (res.status === 204) continue;
      if (res.status === 404) throw new RequestNotFoundError(requestId);
      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }

    throw new ResultTimeoutError(requestId, this.maxRetries);
  }

  /**
   * Single-request fast path using POST /request/sync.
   *
   * Returns `{ result }` when the local server responds before the timeout,
   * or `{ requestId }` when it is slow — caller should then call {@link waitResult}.
   *
   * @param options.key  Route to a named queue (empty → "default").
   * @throws {QueueFullError} if the queue is at capacity.
   */
  async doSync(
    payload: unknown,
    headers: Record<string, string> = {},
    options: RequestOptions = {}
  ): Promise<{ result: Result } | { requestId: string }> {
    const timeoutMs = options.timeout ?? this.longPollTimeoutMs;
    const timeoutSec = (timeoutMs / 1000).toFixed(1);
    const url = `${this.baseUrl}/request/sync?timeout=${timeoutSec}s`;

    const body: Record<string, unknown> = { payload, headers };
    if (options.key) body["key"] = options.key;

    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Online-API-Key": this.apiKey,
      },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(timeoutMs + 10_000),
    });

    if (res.status === 200) return { result: new Result((await res.json()) as ResultData) };
    if (res.status === 202) {
      const data = (await res.json()) as { id: string };
      return { requestId: data.id };
    }
    if (res.status === 503) {
      const data = (await res.json()) as { message?: string };
      throw new QueueFullError(data.message);
    }
    throw new HubRouterError(
      `unexpected status ${res.status}: ${await res.text()}`
    );
  }

  /**
   * Submit a request and wait for its result.
   * Tries the sync fast path first; falls back to async polling if the local server is slow.
   *
   * @param options.key  Route to a named queue (empty → "default").
   */
  async do(
    payload: unknown,
    headers: Record<string, string> = {},
    options: RequestOptions = {}
  ): Promise<Result> {
    const response = await this.doSync(payload, headers, options);
    if ("result" in response) return response.result;
    return this.waitResult(response.requestId, { timeout: options.timeout });
  }

  /**
   * Forward an incoming HTTP request verbatim to hub-router.
   *
   * The original headers and body are passed through to the local server unchanged.
   * Hop-by-hop headers (Content-Length, Host, Transfer-Encoding, etc.) are excluded.
   *
   * Compatible with the Web API `Request` object (Node.js 18+, Deno, Cloudflare Workers).
   *
   * @param options.key  Route to a named queue (empty → "default").
   *
   * ```ts
   * app.post("/api/infer", async (req, res) => {
   *   const webReq = new Request(req.url, { method: req.method, headers: req.headers, body: req });
   *   const result = await client.doRequest(webReq, { key: "gpu" });
   *   res.json(result.payload);
   * });
   * ```
   */
  async doRequest(
    request: Request,
    options: RequestOptions = {}
  ): Promise<Result> {
    const headers: Record<string, string> = {};
    request.headers.forEach((value, key) => {
      if (!HOP_BY_HOP.has(key.toLowerCase())) {
        headers[key] = value;
      }
    });

    const payload = await request.text();
    const response = await this.doSync(payload, headers, options);
    if ("result" in response) return response.result;
    return this.waitResult(response.requestId, { timeout: options.timeout });
  }
}

// ─── LocalClient ─────────────────────────────────────────────────────────────

const DEFAULT_BATCH_SIZE = 10;
const DEFAULT_POLL_INTERVAL_MS = 1_000;
const DEFAULT_WORKERS = 1;

/**
 * LocalClient — poll hub-router for requests, process them, push results back.
 * Fully async, non-blocking. The poll loop runs as an independent Promise.
 *
 * ```ts
 * // Pull only from the "gpu" queue
 * const client = new LocalClient("https://hub.example.com", "local-key", { key: "gpu" });
 *
 * // Fire-and-forget alongside your server
 * client.run(async (req) => {
 *   const answer = await myModel.infer(req.payload);
 *   return { request_id: req.id, payload: answer, status_code: 200 };
 * });
 * ```
 */
export class LocalClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly key: string;
  private readonly batchSize: number;
  private readonly pollIntervalMs: number;
  private readonly workers: number;
  private stopped = false;

  /**
   * @param baseUrl  hub-router base URL, e.g. "https://hub.example.com"
   * @param apiKey   X-Local-API-Key value
   * @param options  Optional overrides (including `key` for named queue)
   */
  constructor(
    baseUrl: string,
    apiKey: string,
    options: LocalClientOptions = {}
  ) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
    this.apiKey = apiKey;
    this.key = options.key ?? "";
    this.batchSize = options.batchSize ?? DEFAULT_BATCH_SIZE;
    this.pollIntervalMs = options.pollInterval ?? DEFAULT_POLL_INTERVAL_MS;
    this.workers = options.workers ?? DEFAULT_WORKERS;
  }

  /**
   * Start the poll loop. Returns a Promise that resolves when {@link stop} is called.
   *
   * Non-blocking — run alongside your server:
   * ```ts
   * client.run(processor);    // no await — runs in background
   * await server.listen(3000);
   * ```
   */
  async run(processor: ProcessorFn): Promise<void> {
    let activeWorkers = 0;
    const waitForWorker = (): Promise<void> =>
      new Promise((resolve) => {
        const check = () => {
          if (activeWorkers < this.workers) resolve();
          else setImmediate(check);
        };
        check();
      });

    while (!this.stopped) {
      let requests: QueuedRequest[];
      try {
        requests = await this.pullBatch();
      } catch {
        await this._sleep(this.pollIntervalMs);
        continue;
      }

      if (requests.length === 0) {
        await this._sleep(this.pollIntervalMs);
        continue;
      }

      for (const req of requests) {
        await waitForWorker();
        if (this.stopped) break;
        activeWorkers++;
        this._processAndPush(processor, req).finally(() => {
          activeWorkers--;
        });
      }
    }

    while (activeWorkers > 0) {
      await this._sleep(50);
    }
  }

  /** Signal the poll loop to stop after the current batch finishes. */
  stop(): void {
    this.stopped = true;
  }

  /** Fetch up to `batchSize` pending requests for this client's key. */
  async pullBatch(): Promise<QueuedRequest[]> {
    let url = `${this.baseUrl}/queue/pull?batch=${this.batchSize}`;
    if (this.key) url += `&key=${encodeURIComponent(this.key)}`;
    const res = await fetch(url, {
      headers: { "X-Local-API-Key": this.apiKey },
    });
    if (res.status !== 200) {
      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }
    const data = (await res.json()) as PullResponse;
    return data.requests ?? [];
  }

  /** Push a completed result to hub-router. */
  async pushResult(result: ResultData): Promise<void> {
    const body: ResultData = {
      ...result,
      completed_at: result.completed_at ?? new Date().toISOString(),
    };
    const res = await fetch(`${this.baseUrl}/queue/result`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Local-API-Key": this.apiKey,
      },
      body: JSON.stringify(body),
    });
    if (res.status === 404) throw new RequestNotFoundError(result.request_id);
    if (res.status !== 204) {
      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }
  }

  private async _processAndPush(
    processor: ProcessorFn,
    req: QueuedRequest
  ): Promise<void> {
    let result: ResultData;
    try {
      result = await processor(req);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      result = {
        request_id: req.id,
        payload: { error: message },
        status_code: 500,
        error: message,
      };
    }
    try {
      await this.pushResult(result);
    } catch {
      // Best-effort: request may have expired.
    }
  }

  private _sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}
