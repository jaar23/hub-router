/**
 * LocalClient — poll hub-router for requests, process them, push results back.
 * Fully async, non-blocking. The poll loop runs as an independent Promise so it
 * never blocks your application's event loop.
 */
import { HubRouterError, RequestNotFoundError } from "./errors.js";
import type {
  LocalClientOptions,
  ProcessorFn,
  PullResponse,
  QueuedRequest,
  Result,
} from "./types.js";

const DEFAULT_BATCH_SIZE = 10;
const DEFAULT_POLL_INTERVAL_MS = 1_000;
const DEFAULT_WORKERS = 1;

export class LocalClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly batchSize: number;
  private readonly pollIntervalMs: number;
  private readonly workers: number;
  private stopped = false;

  /**
   * @param baseUrl   hub-router base URL, e.g. "https://hub.example.com"
   * @param apiKey    X-Local-API-Key value
   * @param options   Optional overrides
   */
  constructor(
    baseUrl: string,
    apiKey: string,
    options: LocalClientOptions = {}
  ) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
    this.apiKey = apiKey;
    this.batchSize = options.batchSize ?? DEFAULT_BATCH_SIZE;
    this.pollIntervalMs = options.pollInterval ?? DEFAULT_POLL_INTERVAL_MS;
    this.workers = options.workers ?? DEFAULT_WORKERS;
  }

  /**
   * Start the poll loop. Returns a Promise that resolves when {@link stop} is called.
   *
   * **Non-blocking usage** — run alongside your server without await:
   * ```ts
   * client.run(processor);          // fire and forget
   * await server.listen(3000);      // your server keeps running
   * ```
   *
   * **Blocking usage** — run as the sole task:
   * ```ts
   * await client.run(processor);
   * ```
   */
  async run(processor: ProcessorFn): Promise<void> {
    // Simple semaphore: track active worker count.
    let activeWorkers = 0;
    const waitForWorker = (): Promise<void> =>
      new Promise((resolve) => {
        const check = () => {
          if (activeWorkers < this.workers) {
            resolve();
          } else {
            setImmediate(check);
          }
        };
        check();
      });

    while (!this.stopped) {
      let requests: QueuedRequest[];
      try {
        requests = await this.pullBatch();
      } catch {
        // Transient error — wait and retry.
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
        // Dispatch without await — run concurrently, bounded by semaphore.
        this._processAndPush(processor, req).finally(() => {
          activeWorkers--;
        });
      }
    }

    // Drain: wait for all active workers to finish.
    while (activeWorkers > 0) {
      await this._sleep(50);
    }
  }

  /** Signal the poll loop to stop after the current batch finishes. */
  stop(): void {
    this.stopped = true;
  }

  /** Fetch up to `batchSize` pending requests. Never blocks the event loop. */
  async pullBatch(): Promise<QueuedRequest[]> {
    const res = await fetch(
      `${this.baseUrl}/queue/pull?batch=${this.batchSize}`,
      { headers: { "X-Local-API-Key": this.apiKey } }
    );

    if (res.status !== 200) {
      throw new HubRouterError(
        `unexpected status ${res.status}: ${await res.text()}`
      );
    }

    const data = (await res.json()) as PullResponse;
    return data.requests ?? [];
  }

  /** Push a completed result to hub-router. */
  async pushResult(result: Result): Promise<void> {
    const body: Result = {
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

    if (res.status === 404) {
      throw new RequestNotFoundError(result.request_id);
    }
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
    let result: Result;
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
