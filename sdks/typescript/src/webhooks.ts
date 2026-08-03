// Receiver-side verification of pushq deliveries (Standard Webhooks scheme:
// HMAC-SHA256 over "id.timestamp.payload"). Uses Web Crypto, so it works in
// Node 18+, Bun, Deno, and edge runtimes.

export class WebhookVerificationError extends Error {
  constructor(message: string) {
    super(`pushq webhook verification failed: ${message}`);
    this.name = "WebhookVerificationError";
  }
}

export interface VerifyOptions {
  /** The queue's signing secret ("whsec_…" or raw). */
  secret: string;
  /** Incoming request headers (Headers, or a plain object). */
  headers: Headers | Record<string, string | string[] | undefined>;
  /** The raw request body, exactly as received. */
  payload: string | Uint8Array;
  /** Reject timestamps further than this from now. Default 300s; 0 disables. */
  toleranceSeconds?: number;
  /** Override "now" (tests). */
  now?: Date;
}

/**
 * Verifies a pushq delivery. Resolves with the webhook id ("queue/task_id",
 * stable across retries — usable as an idempotency key) or throws
 * WebhookVerificationError.
 */
export async function verifySignature(opts: VerifyOptions): Promise<string> {
  const get = headerGetter(opts.headers);
  const id = get("webhook-id");
  const tsRaw = get("webhook-timestamp");
  const sigHeader = get("webhook-signature");
  if (!id || !tsRaw || !sigHeader) {
    throw new WebhookVerificationError("missing webhook-id/-timestamp/-signature headers");
  }

  const ts = Number(tsRaw);
  if (!Number.isFinite(ts)) throw new WebhookVerificationError("invalid timestamp");
  const tolerance = opts.toleranceSeconds ?? 300;
  if (tolerance > 0) {
    const now = (opts.now ?? new Date()).getTime() / 1000;
    if (Math.abs(now - ts) > tolerance) {
      throw new WebhookVerificationError("timestamp outside tolerance");
    }
  }

  const payload =
    typeof opts.payload === "string" ? new TextEncoder().encode(opts.payload) : opts.payload;
  const prefix = new TextEncoder().encode(`${id}.${ts}.`);
  const message = new Uint8Array(prefix.length + payload.length);
  message.set(prefix);
  message.set(payload, prefix.length);

  const key = await crypto.subtle.importKey(
    "raw", signingKey(opts.secret), { name: "HMAC", hash: "SHA-256" }, false, ["verify"],
  );

  for (const part of sigHeader.split(/\s+/)) {
    const [version, sig] = splitOnce(part, ",");
    if (version !== "v1" || !sig) continue;
    let sigBytes: Uint8Array;
    try {
      sigBytes = b64decode(sig);
    } catch {
      continue;
    }
    // crypto.subtle.verify is constant-time by construction.
    if (await crypto.subtle.verify("HMAC", key, toArrayBuffer(sigBytes), toArrayBuffer(message))) {
      return id;
    }
  }
  throw new WebhookVerificationError("no matching signature");
}

/** Computes a "v1,<base64>" signature (test/tooling helper). */
export async function sign(
  secret: string, msgId: string, timestampSeconds: number, payload: string | Uint8Array,
): Promise<string> {
  const body = typeof payload === "string" ? new TextEncoder().encode(payload) : payload;
  const prefix = new TextEncoder().encode(`${msgId}.${timestampSeconds}.`);
  const message = new Uint8Array(prefix.length + body.length);
  message.set(prefix);
  message.set(body, prefix.length);
  const key = await crypto.subtle.importKey(
    "raw", signingKey(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"],
  );
  const mac = new Uint8Array(await crypto.subtle.sign("HMAC", key, toArrayBuffer(message)));
  return `v1,${b64encode(mac)}`;
}

function signingKey(secret: string): ArrayBuffer {
  if (secret.startsWith("whsec_")) {
    return toArrayBuffer(b64decode(secret.slice("whsec_".length)));
  }
  return toArrayBuffer(new TextEncoder().encode(secret));
}

function headerGetter(
  headers: Headers | Record<string, string | string[] | undefined>,
): (name: string) => string | undefined {
  if (typeof (headers as Headers).get === "function") {
    return (name) => (headers as Headers).get(name) ?? undefined;
  }
  const lower: Record<string, string> = {};
  for (const [k, v] of Object.entries(headers as Record<string, string | string[] | undefined>)) {
    if (v === undefined) continue;
    lower[k.toLowerCase()] = Array.isArray(v) ? v[0] : v;
  }
  return (name) => lower[name.toLowerCase()];
}

function splitOnce(s: string, sep: string): [string, string | undefined] {
  const i = s.indexOf(sep);
  return i === -1 ? [s, undefined] : [s.slice(0, i), s.slice(i + 1)];
}

function b64decode(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function b64encode(data: Uint8Array): string {
  let bin = "";
  for (const b of data) bin += String.fromCharCode(b);
  return btoa(bin);
}

function toArrayBuffer(view: Uint8Array): ArrayBuffer {
  return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer;
}
