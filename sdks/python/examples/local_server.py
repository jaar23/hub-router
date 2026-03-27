"""
Example: local processing server using hub-router Python SDK.

Install:
    pip install httpx

Run (with hub-router already running):
    python local_server.py
"""
import asyncio
import signal

from hub_router import LocalClient, QueuedRequest, Result


async def processor(req: QueuedRequest) -> Result:
    """
    Your processing logic — replace with real work (ML inference, DB queries, etc.).
    This is a coroutine so it never blocks the event loop.
    """
    print(f"Processing {req.id}: {req.payload}")

    # Simulate async work.
    await asyncio.sleep(0.2)

    answer = {
        "input": req.payload,
        "answer": "42",
    }
    return Result(request_id=req.id, payload=answer, status_code=200)


async def main():
    client = LocalClient(
        "http://localhost:8080",
        api_key="local-secret",
        batch_size=20,
        poll_interval=0.5,
        workers=4,
    )

    loop = asyncio.get_running_loop()
    stop = asyncio.Event()

    def _signal_handler():
        print("\nShutting down...")
        stop.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, _signal_handler)

    print("Local server starting — polling hub-router...")

    # Run the poll loop as a background task so we can also watch the stop event.
    task = asyncio.create_task(client.run(processor))

    await stop.wait()
    client._stop_event.set()
    await task
    print("Done.")


if __name__ == "__main__":
    asyncio.run(main())
