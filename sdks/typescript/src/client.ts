import type {
  Attempt, BatchResult, ListQueuesParams, ListTasksParams, Page, Queue,
  QueueConfig, QueuePatch, QueueStats, Task, TaskCreate,
} from "./types.js";
import {
  attemptFromWire, batchResultFromWire, queueConfigToWire, queueFromWire,
  statsFromWire, taskCreateToWire, taskFromWire,
} from "./wire.js";

/** Error thrown for any non-2xx API response (RFC 9457 problem+json). */
export class PushqError extends Error {
  readonly status: number;
  /** Stable machine code: already_exists, not_found, failed_precondition, … */
  readonly code: string;
  readonly detail: string;

  constructor(status: number, code: string, detail: string) {
    super(`pushq: ${code} (${status}): ${detail}`);
    this.name = "PushqError";
    this.status = status;
    this.code = code;
    this.detail = detail;
  }
}

/** The hosted pushq API, used when no baseUrl is given. */
export const DEFAULT_BASE_URL = "https://api.pushqueue.dev";

export interface PushqOptions {
  /**
   * Your pushq server, e.g. "http://localhost:8080" when self-hosting.
   * Defaults to the hosted API, {@link DEFAULT_BASE_URL}.
   */
  baseUrl?: string;
  apiKey?: string;
  /** Per-request timeout in ms. Default 30_000. */
  timeoutMs?: number;
  /** Custom fetch (tests, polyfills). Defaults to globalThis.fetch. */
  fetch?: typeof fetch;
}

export class Pushq {
  private readonly base: string;
  private readonly apiKey?: string;
  private readonly timeoutMs: number;
  private readonly fetchFn: typeof fetch;

  constructor(opts: PushqOptions = {}) {
    this.base = (opts.baseUrl?.trim() || DEFAULT_BASE_URL).replace(/\/+$/, "");
    this.apiKey = opts.apiKey;
    this.timeoutMs = opts.timeoutMs ?? 30_000;
    this.fetchFn = opts.fetch ?? globalThis.fetch;
    if (!this.fetchFn) throw new Error("pushq: no fetch available; pass options.fetch");
  }

  // --- queues ---

  /** Create or replace queue config (idempotent; never changes run state). */
  async upsertQueue(name: string, cfg: QueueConfig = {}): Promise<Queue> {
    return queueFromWire(await this.request("PUT", `/v1/queues/${enc(name)}`, queueConfigToWire(cfg)));
  }

  /** Create a queue; rejects with already_exists if present. */
  async createQueue(name: string, cfg: QueueConfig = {}): Promise<Queue> {
    return queueFromWire(await this.request("POST", `/v1/queues`, { name, ...queueConfigToWire(cfg) }));
  }

  async getQueue(name: string): Promise<Queue> {
    return queueFromWire(await this.request("GET", `/v1/queues/${enc(name)}`));
  }

  /** Update only the provided fields (can set state, including "disabled"). */
  async patchQueue(name: string, patch: QueuePatch): Promise<Queue> {
    return queueFromWire(await this.request("PATCH", `/v1/queues/${enc(name)}`, queueConfigToWire(patch)));
  }

  /** Admin role required. */
  async listQueues(params: ListQueuesParams = {}): Promise<Page<Queue>> {
    const w: any = await this.request("GET", `/v1/queues${query({
      name_prefix: params.namePrefix,
      page_size: params.pageSize,
      page_token: params.pageToken,
    })}`);
    return { items: (w.queues ?? []).map(queueFromWire), nextPageToken: w.next_page_token || undefined };
  }

  /** Admin role required. Deletes the queue and every task in it. */
  async deleteQueue(name: string): Promise<void> {
    await this.request("DELETE", `/v1/queues/${enc(name)}`);
  }

  async pauseQueue(name: string): Promise<Queue> {
    return queueFromWire(await this.request("POST", `/v1/queues/${enc(name)}:pause`));
  }

  async resumeQueue(name: string): Promise<Queue> {
    return queueFromWire(await this.request("POST", `/v1/queues/${enc(name)}:resume`));
  }

  /** Deletes every task created up to now (exact watermark; IDs become reusable). */
  async purgeQueue(name: string): Promise<Queue> {
    return queueFromWire(await this.request("POST", `/v1/queues/${enc(name)}:purge`));
  }

  async queueStats(name: string, opts: { idPrefix?: string } = {}): Promise<QueueStats> {
    return statsFromWire(await this.request("GET",
      `/v1/queues/${enc(name)}/stats${query({ id_prefix: opts.idPrefix })}`));
  }

  // --- tasks ---

  async createTask(queue: string, task: TaskCreate): Promise<Task> {
    return taskFromWire(await this.request("POST", `/v1/queues/${enc(queue)}/tasks`, taskCreateToWire(task)));
  }

  /** Enqueue up to 500 tasks; non-atomic, per-item results in order. */
  async createTasks(queue: string, tasks: TaskCreate[]): Promise<BatchResult[]> {
    const w: any = await this.request("POST", `/v1/queues/${enc(queue)}/tasks:batch`, {
      tasks: tasks.map(taskCreateToWire),
    });
    return (w.results ?? []).map(batchResultFromWire);
  }

  async getTask(queue: string, id: string, opts: { view?: "basic" | "full" } = {}): Promise<Task> {
    return taskFromWire(await this.request("GET",
      `/v1/queues/${enc(queue)}/tasks/${enc(id)}${query({ view: opts.view })}`));
  }

  async listTasks(queue: string, params: ListTasksParams = {}): Promise<Page<Task>> {
    const w: any = await this.request("GET", `/v1/queues/${enc(queue)}/tasks${query({
      state: params.state,
      id_prefix: params.idPrefix,
      view: params.view,
      page_size: params.pageSize,
      page_token: params.pageToken,
    })}`);
    return { items: (w.tasks ?? []).map(taskFromWire), nextPageToken: w.next_page_token || undefined };
  }

  /** Deletes a task; the ID stays reserved for the dedup window. */
  async deleteTask(queue: string, id: string): Promise<void> {
    await this.request("DELETE", `/v1/queues/${enc(queue)}/tasks/${enc(id)}`);
  }

  /** Force immediate dispatch, bypassing rate limits and PAUSED state. */
  async runTask(queue: string, id: string): Promise<Task> {
    return taskFromWire(await this.request("POST", `/v1/queues/${enc(queue)}/tasks/${enc(id)}:run`));
  }

  async listAttempts(queue: string, id: string): Promise<Attempt[]> {
    const w: any = await this.request("GET", `/v1/queues/${enc(queue)}/tasks/${enc(id)}/attempts`);
    return (w.attempts ?? []).map(attemptFromWire);
  }

  // --- plumbing ---

  private async request(method: string, path: string, body?: unknown): Promise<any> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (this.apiKey) headers.Authorization = `Bearer ${this.apiKey}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const resp = await this.fetchFn(this.base + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(this.timeoutMs),
    });

    if (resp.status === 204) return undefined;
    const text = await resp.text();
    let parsed: any;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      parsed = undefined;
    }
    if (!resp.ok) {
      throw new PushqError(
        resp.status,
        parsed?.code ?? "internal",
        parsed?.detail ?? parsed?.title ?? text.slice(0, 200) ?? resp.statusText,
      );
    }
    return parsed;
  }
}

const enc = encodeURIComponent;

function query(params: Record<string, string | number | undefined>): string {
  const parts: string[] = [];
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") parts.push(`${k}=${encodeURIComponent(String(v))}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}
