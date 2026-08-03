// Conversion between the snake_case wire format and the camelCase SDK types.
import type {
  Attempt, BatchResult, Queue, QueueConfig, QueuePatch, QueueStats, RetryConfig,
  Task, TaskCreate,
} from "./types.js";

const toB64 = (data: Uint8Array): string => {
  let bin = "";
  for (const b of data) bin += String.fromCharCode(b);
  return btoa(bin);
};

const fromB64 = (s: string): Uint8Array => {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
};

const date = (s: string | undefined): Date | undefined => (s ? new Date(s) : undefined);

export function retryToWire(r: RetryConfig | undefined): Record<string, unknown> | undefined {
  if (!r) return undefined;
  return prune({
    max_attempts: r.maxAttempts,
    max_retry_duration_seconds: r.maxRetryDurationSeconds,
    min_backoff_seconds: r.minBackoffSeconds,
    max_backoff_seconds: r.maxBackoffSeconds,
    max_doublings: r.maxDoublings,
  });
}

function retryFromWire(w: any): RetryConfig | undefined {
  if (!w) return undefined;
  return {
    maxAttempts: w.max_attempts,
    maxRetryDurationSeconds: w.max_retry_duration_seconds,
    minBackoffSeconds: w.min_backoff_seconds,
    maxBackoffSeconds: w.max_backoff_seconds,
    maxDoublings: w.max_doublings,
  };
}

function prune(obj: Record<string, unknown>): Record<string, unknown> | undefined {
  const out: Record<string, unknown> = {};
  let any = false;
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined) {
      out[k] = v;
      any = true;
    }
  }
  return any ? out : undefined;
}

export function queueConfigToWire(cfg: QueueConfig | QueuePatch): Record<string, unknown> {
  const target = cfg.target
    ? prune({
        base_url: cfg.target.baseUrl,
        method: cfg.target.method,
        headers: cfg.target.headers,
        signing_secret: cfg.target.signingSecret,
      })
    : undefined;
  const rateLimits = cfg.rateLimits
    ? prune({
        max_dispatches_per_second: cfg.rateLimits.maxDispatchesPerSecond,
        max_burst_size: cfg.rateLimits.maxBurstSize,
        max_concurrent_dispatches: cfg.rateLimits.maxConcurrentDispatches,
      })
    : undefined;
  return prune({
    state: (cfg as QueuePatch).state,
    rate_limits: rateLimits,
    retry: retryToWire(cfg.retry),
    target,
    dead_letter_queue: cfg.deadLetterQueue,
  }) ?? {};
}

export function queueFromWire(w: any): Queue {
  return {
    name: w.name,
    state: w.state,
    rateLimits: {
      maxDispatchesPerSecond: w.rate_limits.max_dispatches_per_second,
      maxBurstSize: w.rate_limits.max_burst_size,
      maxConcurrentDispatches: w.rate_limits.max_concurrent_dispatches,
    },
    retry: retryFromWire(w.retry) as Required<RetryConfig>,
    target: {
      baseUrl: w.target?.base_url,
      method: w.target?.method,
      headers: w.target?.headers,
      hasSigningSecret: !!w.target?.has_signing_secret,
    },
    deadLetterQueue: w.dead_letter_queue,
    purgedAt: date(w.purged_at),
    createdAt: new Date(w.created_at),
    updatedAt: new Date(w.updated_at),
  };
}

export function statsFromWire(w: any): QueueStats {
  return {
    state: w.state,
    pending: w.pending,
    running: w.running,
    succeeded: w.succeeded,
    failed: w.failed,
    scheduledFuture: w.scheduled_future,
    oldestDueAt: date(w.oldest_due_at),
    oldestDueAgeSeconds: w.oldest_due_age_seconds,
    dispatchedLast1m: w.dispatched_last_1m,
    errorsLast1m: w.errors_last_1m,
    dispatchedLast1h: w.dispatched_last_1h,
    errorsLast1h: w.errors_last_1h,
    lastPurge: date(w.last_purge),
  };
}

export function taskCreateToWire(t: TaskCreate): Record<string, unknown> {
  if (t.body !== undefined && t.jsonBody !== undefined) {
    throw new Error("pushq: provide body or jsonBody, not both");
  }
  let bodyBase64: string | undefined;
  if (typeof t.body === "string") {
    bodyBase64 = toB64(new TextEncoder().encode(t.body));
  } else if (t.body instanceof Uint8Array) {
    bodyBase64 = toB64(t.body);
  }
  const schedule =
    t.scheduleTime instanceof Date ? t.scheduleTime.toISOString() : t.scheduleTime;
  return prune({
    task_id: t.taskId,
    url: t.url,
    path: t.path,
    method: t.method,
    headers: t.headers,
    body_base64: bodyBase64,
    json_body: t.jsonBody,
    schedule_time: schedule,
    delay_seconds: t.delaySeconds,
    dispatch_deadline_seconds: t.dispatchDeadlineSeconds,
    retry: retryToWire(t.retry),
  }) ?? {};
}

export function taskFromWire(w: any): Task {
  return {
    id: w.id,
    queue: w.queue,
    state: w.state,
    url: w.url,
    method: w.method,
    headers: w.headers,
    body: w.body_base64 ? fromB64(w.body_base64) : undefined,
    scheduleTime: new Date(w.schedule_time),
    createdAt: new Date(w.created_at),
    dispatchDeadlineSeconds: w.dispatch_deadline_seconds,
    retry: retryFromWire(w.retry),
    dispatchCount: w.dispatch_count,
    responseCount: w.response_count,
    firstAttemptAt: date(w.first_attempt_at),
    finishedAt: date(w.finished_at),
    lastAttempt: w.last_attempt
      ? { at: new Date(w.last_attempt.at), status: w.last_attempt.status, error: w.last_attempt.error }
      : undefined,
  };
}

export function attemptFromWire(w: any): Attempt {
  return {
    number: w.number,
    dispatchTime: new Date(w.dispatch_time),
    responseTime: date(w.response_time),
    responseStatus: w.response_status,
    errorKind: w.error_kind,
    errorSnippet: w.error_snippet,
    durationMs: w.duration_ms,
  };
}

export function batchResultFromWire(w: any): BatchResult {
  return {
    index: w.index,
    task: w.task ? taskFromWire(w.task) : undefined,
    error: w.error,
  };
}
