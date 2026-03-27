"""
Example: online server (FastAPI) using hub-router Python SDK.

Install:
    pip install fastapi uvicorn httpx

Run (with hub-router already running):
    uvicorn online_server:app --reload
"""
import asyncio
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from hub_router import OnlineClient, QueueFullError, ResultTimeoutError


class QueryRequest(BaseModel):
    input: str


class QueryResponse(BaseModel):
    request_id: str
    answer: dict


# Shared client — created at startup, closed at shutdown.
hub_client: OnlineClient


@asynccontextmanager
async def lifespan(app: FastAPI):
    global hub_client
    hub_client = OnlineClient(
        "http://localhost:8080",
        api_key="online-secret",
        long_poll_timeout=30.0,
        max_retries=5,
    )
    yield
    await hub_client._client.aclose()


app = FastAPI(title="Online Server Example", lifespan=lifespan)


@app.post("/query", response_model=QueryResponse)
async def query(body: QueryRequest):
    """Submit a query to the local server via hub-router and return the result."""
    try:
        # Non-blocking: this coroutine suspends while waiting; other requests proceed.
        result = await hub_client.do({"input": body.input})
    except QueueFullError:
        raise HTTPException(status_code=503, detail="processing queue is full")
    except ResultTimeoutError:
        raise HTTPException(status_code=504, detail="local server did not respond in time")

    return QueryResponse(request_id=result.request_id, answer=result.payload or {})


# --- Standalone async demo (no FastAPI) ---
async def standalone_demo():
    async with OnlineClient("http://localhost:8080", api_key="online-secret") as client:
        print("Submitting request...")
        result = await client.do({"input": "hello world"})
        print(f"Result (status={result.status_code}): {result.payload}")

        # Fan-out example
        print("\nFan-out: 3 concurrent requests")
        ids = await asyncio.gather(*[
            client.submit({"n": i}) for i in range(3)
        ])
        results = await asyncio.gather(*[
            client.wait_result(rid) for rid in ids
        ])
        for r in results:
            print(f"  {r.request_id}: {r.payload}")


if __name__ == "__main__":
    asyncio.run(standalone_demo())
