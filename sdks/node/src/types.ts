/** Shared types for the hub-router Node.js SDK. */

/** A pending request pulled by the local server. */
export interface QueuedRequest {
  id: string;
  payload: unknown;
  headers: Record<string, string>;
  enqueued_at: string;
  expires_at: string;
}

/** A processed result pushed back by the local server. */
export interface Result {
  request_id: string;
  payload: unknown;
  status_code: number;
  error?: string;
  completed_at?: string;
}

/** Response from POST /request. */
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
  /** Requests per poll cycle (default: 10). */
  batchSize?: number;
  /** Sleep in ms when queue is empty (default: 1000). */
  pollInterval?: number;
  /** Max concurrent processor calls (default: 1). */
  workers?: number;
}

/** Processor callback: receives a request, returns a result. */
export type ProcessorFn = (req: QueuedRequest) => Promise<Result>;
