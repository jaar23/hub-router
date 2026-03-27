/**
 * Example: local processing server using hub-router Node.js SDK.
 *
 * Run (with hub-router already running):
 *   npx ts-node --esm local-server.ts
 */
import { LocalClient } from "../src/index.js";
import type { ProcessorFn, QueuedRequest, Result } from "../src/types.js";

const processor: ProcessorFn = async (req: QueuedRequest): Promise<Result> => {
  console.log(`Processing ${req.id}:`, req.payload);

  // Simulate async work — replace with real logic.
  await new Promise((resolve) => setTimeout(resolve, 200));

  return {
    request_id: req.id,
    payload: { input: req.payload, answer: "42" },
    status_code: 200,
  };
};

const client = new LocalClient("http://localhost:8080", "local-secret", {
  batchSize: 20,
  pollInterval: 500,
  workers: 4,
});

console.log("Local server starting — polling hub-router...");

// Non-blocking: the run() Promise runs alongside other application code.
const runPromise = client.run(processor);

// Graceful shutdown on Ctrl-C.
process.on("SIGINT", () => {
  console.log("\nShutting down...");
  client.stop();
});
process.on("SIGTERM", () => client.stop());

runPromise
  .then(() => {
    console.log("Done.");
    process.exit(0);
  })
  .catch((err) => {
    console.error("Error:", err);
    process.exit(1);
  });
