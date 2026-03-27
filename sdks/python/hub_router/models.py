"""Shared data models for hub-router Python SDK."""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


@dataclass
class QueuedRequest:
    """A pending request pulled by the local server."""
    id: str
    payload: Any
    headers: dict[str, str] = field(default_factory=dict)
    enqueued_at: datetime | None = None
    expires_at: datetime | None = None

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
    completed_at: datetime | None = None

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


@dataclass
class SubmitResponse:
    id: str
    estimated_wait_ms: int = 0


def _parse_dt(value: str | None) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except (ValueError, AttributeError):
        return None
