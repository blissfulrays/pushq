import { describe, expect, it } from "vitest";
import { sign, verifySignature, WebhookVerificationError } from "../src/webhooks.js";

const secret = "whsec_dGVzdC1zZWNyZXQ="; // "test-secret"
const id = "orders/job-1";
const ts = 1_700_000_000;
const payload = JSON.stringify({ hello: "world" });

async function headersFor(body: string, at = ts) {
  return {
    "webhook-id": id,
    "webhook-timestamp": String(at),
    "webhook-signature": await sign(secret, id, at, body),
  };
}

describe("verifySignature", () => {
  it("verifies a valid signature and returns the id", async () => {
    const got = await verifySignature({
      secret,
      headers: await headersFor(payload),
      payload,
      now: new Date(ts * 1000),
    });
    expect(got).toBe(id);
  });

  it("rejects a tampered payload", async () => {
    await expect(
      verifySignature({
        secret,
        headers: await headersFor(payload),
        payload: payload + "x",
        now: new Date(ts * 1000),
      }),
    ).rejects.toThrow(WebhookVerificationError);
  });

  it("rejects the wrong secret", async () => {
    await expect(
      verifySignature({
        secret: "whsec_d3Jvbmctc2VjcmV0",
        headers: await headersFor(payload),
        payload,
        now: new Date(ts * 1000),
      }),
    ).rejects.toThrow(/no matching signature/);
  });

  it("rejects timestamps outside tolerance", async () => {
    await expect(
      verifySignature({
        secret,
        headers: await headersFor(payload),
        payload,
        now: new Date((ts + 3600) * 1000),
      }),
    ).rejects.toThrow(/tolerance/);
  });

  it("accepts any matching sig in a space-separated list", async () => {
    const good = await sign(secret, id, ts, payload);
    const headers = {
      "webhook-id": id,
      "webhook-timestamp": String(ts),
      "webhook-signature": `v1,AAAA ${good}`,
    };
    await expect(
      verifySignature({ secret, headers, payload, now: new Date(ts * 1000) }),
    ).resolves.toBe(id);
  });

  it("supports raw (non-whsec) secrets", async () => {
    const raw = "plain-secret";
    const headers = {
      "webhook-id": id,
      "webhook-timestamp": String(ts),
      "webhook-signature": await sign(raw, id, ts, payload),
    };
    await expect(
      verifySignature({ secret: raw, headers, payload, now: new Date(ts * 1000) }),
    ).resolves.toBe(id);
  });
});
