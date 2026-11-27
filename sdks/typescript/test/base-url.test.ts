import { describe, expect, it } from "vitest";
import { DEFAULT_BASE_URL, Pushq } from "../src/index.js";

function recordingFetch(urls: string[]): typeof fetch {
  return (async (input: RequestInfo | URL) => {
    urls.push(String(input));
    return new Response(null, { status: 204 });
  }) as typeof fetch;
}

describe("base URL", () => {
  it("defaults to the hosted API", async () => {
    expect(DEFAULT_BASE_URL).toBe("https://api.pushqueue.dev");
    const urls: string[] = [];
    await new Pushq({ apiKey: "k", fetch: recordingFetch(urls) }).deleteQueue("emails");
    await new Pushq({ baseUrl: "  ", fetch: recordingFetch(urls) }).deleteQueue("emails");
    expect(urls).toEqual([
      "https://api.pushqueue.dev/v1/queues/emails",
      "https://api.pushqueue.dev/v1/queues/emails",
    ]);
  });

  it("uses an explicit base URL", async () => {
    const urls: string[] = [];
    await new Pushq({ baseUrl: "http://localhost:8080/", fetch: recordingFetch(urls) }).deleteQueue("emails");
    expect(urls).toEqual(["http://localhost:8080/v1/queues/emails"]);
  });
});
