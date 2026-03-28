/**
 * hub-router Node.js SDK.
 *
 * For a zero-install single-file version, copy hub-router.ts directly.
 */
export {
  OnlineClient,
  LocalClient,
  HubRouterError,
  QueueFullError,
  RequestNotFoundError,
  ResultTimeoutError,
} from "./hub-router.js";

export type {
  QueuedRequest,
  Result,
  SubmitResponse,
  PullResponse,
  OnlineClientOptions,
  LocalClientOptions,
  ProcessorFn,
} from "./hub-router.js";
