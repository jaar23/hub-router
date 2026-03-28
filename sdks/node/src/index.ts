/**
 * hub-router Node.js SDK.
 *
 * For a zero-install single-file version, copy hub-router.ts directly.
 */
export {
  OnlineClient,
  LocalClient,
  Result,
  HubRouterError,
  QueueFullError,
  RequestNotFoundError,
  ResultTimeoutError,
} from "./hub-router.js";

export type {
  QueuedRequest,
  ResultData,
  SubmitResponse,
  PullResponse,
  OnlineClientOptions,
  LocalClientOptions,
  RequestOptions,
  ProcessorFn,
} from "./hub-router.js";
