// A pushq task receiver in Express with signature verification.
//
// Run:
//   npm install express pushq
//   node index.mjs
//
// Then point a queue at it:
//   curl -X PUT localhost:8080/v1/queues/demo -d '{
//     "target": {"base_url": "http://localhost:9000",
//                "signing_secret": "whsec_dGVzdC1zZWNyZXQ="}}'
import express from "express";
import { verifySignature } from "pushq";

const SECRET = process.env.PUSHQ_SIGNING_SECRET ?? "whsec_dGVzdC1zZWNyZXQ=";
const app = express();
const seen = new Set(); // use a persistent store in production

// Keep the raw body: signatures are computed over the exact bytes.
app.use(express.raw({ type: "*/*" }));

app.post("/tasks/send-email", async (req, res) => {
  let msgId;
  try {
    // msgId is "queue/task_id" — stable across retries.
    msgId = await verifySignature({ secret: SECRET, headers: req.headers, payload: req.body });
  } catch {
    return res.status(401).end();
  }

  // pushq delivers at-least-once: dedupe on the stable id.
  if (seen.has(msgId)) return res.json({ status: "duplicate ignored" });
  seen.add(msgId);

  const payload = JSON.parse(req.body);
  console.log(`sending email to ${payload.to} (attempt ${req.headers["x-pushq-attempt"]})`);

  // Any 2xx marks the task succeeded; non-2xx retries on the queue's schedule.
  res.json({ status: "sent" });
});

app.listen(9000, () => console.log("receiver on :9000"));
