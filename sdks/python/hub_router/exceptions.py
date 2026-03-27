"""Exceptions raised by the hub-router Python SDK."""


class HubRouterError(Exception):
    """Base exception for all hub-router errors."""


class QueueFullError(HubRouterError):
    """Raised when the middleware queue is at capacity."""


class RequestNotFoundError(HubRouterError):
    """Raised when a request ID is not found (expired or never existed)."""


class ResultTimeoutError(HubRouterError):
    """Raised when WaitResult exhausts all retries without receiving a result."""
