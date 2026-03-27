/** Errors raised by the hub-router Node.js SDK. */

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
    super(`result not available after ${retries} retries for request '${requestId}'`);
    this.name = "ResultTimeoutError";
  }
}
