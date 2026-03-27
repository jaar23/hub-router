/**
 * Example: Express online server using hub-router Node.js SDK.
 *
 * Install:
 *   npm install express @types/express
 *
 * Run (with hub-router already running):
 *   npx ts-node --esm online-server.ts
 */
import express from "express";
import { OnlineClient, QueueFullError, ResultTimeoutError } from "../src/index.js";

const client = new OnlineClient("http://localhost:8080", "online-secret", {
  longPollTimeout: 30_000,
  maxRetries: 5,
});

const app = express();
app.use(express.json());

app.post("/query", async (req, res) => {
  try {
    // Non-blocking: Express continues serving other requests while this awaits.
    const result = await client.do(req.body);
    res.json({ request_id: result.request_id, answer: result.payload });
  } catch (err) {
    if (err instanceof QueueFullError) {
      res.status(503).json({ error: "processing queue is full" });
    } else if (err instanceof ResultTimeoutError) {
      res.status(504).json({ error: "local server did not respond in time" });
    } else {
      res.status(500).json({ error: String(err) });
    }
  }
});

app.listen(3000, () => console.log("Online server on :3000"));

// --- Standalone demo ---
async function standaloneDemo() {
  console.log("Submitting request...");
  const result = await client.do({ input: "hello world" });
  console.log(`Result (status=${result.status_code}):`, result.payload);

  // Fan-out: 3 concurrent requests
  console.log("\nFan-out: 3 concurrent requests");
  const ids = await Promise.all(
    [0, 1, 2].map((n) => client.submit({ n }))
  );
  const results = await Promise.all(ids.map((id) => client.waitResult(id)));
  results.forEach((r) =>
    console.log(`  ${r.request_id}:`, r.payload)
  );
}

if (process.argv.includes("--demo")) {
  standaloneDemo().catch(console.error);
}
