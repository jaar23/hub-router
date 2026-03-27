/**
 * OnlineClient — submit requests and wait for results from hub-router.
 * Fully async, non-blocking. Works with Express, Fastify, NestJS, etc.
 */
import {
  HubRouterError,
  QueueFullError,
  RequestNotFoundError,
  ResultTimeoutError,
} from "./errors.js";
import type {
  OnlineClientOptions,
  Result,
  SubmitResponse,
} from "./types.js";

const DEFAULT_LONG_POLL_TIMEOUT_MS = 30_000;
const DEFAULT_MAX_RETRIES = 10;

export class OnlineClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly longPollTimeoutMs: number;
  private readonly maxRetries: number;

  /**
   * @param baseUrl   hub-router base URL, e.g. "https://hub.example.com"
   * @param apiKey    X-Online-API-Key value
   * @param options   Optional overrides
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
   *
   * @throws {QueueFullError} if the queue is at capacity.
   */
  async submit(
    payload: unknown,
    headers: Record<string, string> = {}
  ): Promise<string> {
    const res = await fetch(`${this.baseUrl}/request`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Online-API-Key": this.apiKey,
      },
      body: JSON.stringify({ payload, headers }),
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
   *
   * Each poll hangs for up to `longPollTimeout` ms server-side.
   * A 204 response means the server timed out waiting — this client retries
   * automatically (up to `maxRetries` times).
   *
   * @throws {RequestNotFoundError} if the ID is unknown or expired.
   * @throws {ResultTimeoutError}   if `maxRetries` exhausted.
   */
  async waitResult(requestId: string, options?: { timeout?: number }): Promise<Result> {
    const timeoutMs = options?.timeout ?? this.longPollTimeoutMs;
    const timeoutSec = (timeoutMs / 1000).toFixed(1);
    const url = `${this.baseUrl}/result/${encodeURIComponent(requestId)}?timeout=${timeoutSec}s`;

    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      const res = await fetch(url, {
        headers: { "X-Online-API-Key": this.apiKey },
        signal: AbortSignal.timeout(timeoutMs + 10_000), // safety net
      });

      if (res.status === 200) {
        return (await res.json()) as Result;
      }
      if (res.status === 204) {
        // Server timed out — retry with same ID.
        continue;
      }
      if (res.status === 404) {
        throw new RequestNotFoundError(requestId);
      }

      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }

    throw new ResultTimeoutError(requestId, this.maxRetries);
  }

  /**
   * Convenience wrapper: {@link submit} + {@link waitResult} in one call.
   */
  async do(
    payload: unknown,
    headers: Record<string, string> = {}
  ): Promise<Result> {
    const id = await this.submit(payload, headers);
    return this.waitResult(id);
  }
}
