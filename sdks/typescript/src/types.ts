/** Rate limits for a queue (GCP Cloud Tasks semantics). */
export interface RateLimits {
  /** Tokens per second. Default 500. */
  maxDispatchesPerSecond?: number;
  /** Token bucket burst. 0/omitted = auto: clamp(ceil(rate), 1, 500). */
  maxBurstSize?: number;
  /** Max concurrent in-flight dispatches. Default 1000. */
  maxConcurrentDispatches?: number;
}

/** Retry configuration (GCP Cloud Tasks semantics). */
export interface RetryConfig {
  /** Includes the first attempt; -1 = unlimited. Default 100. */
  maxAttempts?: number;
  /** Seconds from the first attempt; 0 = unlimited. */
  maxRetryDurationSeconds?: number;
  minBackoffSeconds?: number;
  maxBackoffSeconds?: number;
  /** Interval doubles this many times, then grows linearly, capped at maxBackoff. */
  maxDoublings?: number;
}

export interface Target {
  /** Absolute http(s) URL; resolves tasks created with a relative `path`. */
  baseUrl?: string;
  /** Overrides every task's method when set. */
  method?: string;
  /** Merged over task headers at dispatch (queue wins). */
  headers?: Record<string, string>;
  /**
   * Standard Webhooks secret ("whsec_…"). On upsert: undefined = keep the
   * stored secret, "" = clear it.
   */
  signingSecret?: string;
}

export interface QueueConfig {
  rateLimits?: RateLimits;
  retry?: RetryConfig;
  target?: Target;
  deadLetterQueue?: string;
}

export interface QueuePatch extends QueueConfig {
  state?: "running" | "paused" | "disabled";
}

export interface Queue {
  name: string;
  state: "running" | "paused" | "disabled";
  rateLimits: Required<RateLimits>;
  retry: Required<RetryConfig>;
  target: {
    baseUrl?: string;
    method?: string;
    headers?: Record<string, string>;
    hasSigningSecret: boolean;
  };
  deadLetterQueue?: string;
  purgedAt?: Date;
  createdAt: Date;
  updatedAt: Date;
}

export interface QueueStats {
  state: string;
  pending: number;
  running: number;
  succeeded: number;
  failed: number;
  scheduledFuture: number;
  oldestDueAt?: Date;
  oldestDueAgeSeconds?: number;
  dispatchedLast1m: number;
  errorsLast1m: number;
  dispatchedLast1h: number;
  errorsLast1h: number;
  lastPurge?: Date;
}

export interface TaskCreate {
  /** Client-chosen ID for dedup (409 on collision). Omitted = server UUIDv7. */
  taskId?: string;
  /** Absolute http(s) URL. Exactly one of url/path. */
  url?: string;
  /** Path resolved against the queue's target.baseUrl at dispatch time. */
  path?: string;
  method?: string;
  headers?: Record<string, string>;
  /** Raw body bytes or string. Exclusive with jsonBody. POST/PUT/PATCH only. */
  body?: Uint8Array | string;
  /** Any JSON value, sent with Content-Type: application/json. */
  jsonBody?: unknown;
  /** Exclusive with delaySeconds. */
  scheduleTime?: Date | string;
  delaySeconds?: number;
  /** 15–1800; default 600. */
  dispatchDeadlineSeconds?: number;
  /** Per-task override of the queue retry config (any subset). */
  retry?: RetryConfig;
}

export interface Task {
  id: string;
  queue: string;
  state: "pending" | "running" | "succeeded" | "failed";
  url: string;
  method: string;
  headers?: Record<string, string>;
  /** Only populated with view: "full". */
  body?: Uint8Array;
  scheduleTime: Date;
  createdAt: Date;
  dispatchDeadlineSeconds: number;
  retry?: RetryConfig;
  dispatchCount: number;
  responseCount: number;
  firstAttemptAt?: Date;
  finishedAt?: Date;
  lastAttempt?: { at: Date; status?: number; error?: string };
}

export interface Attempt {
  number: number;
  dispatchTime: Date;
  responseTime?: Date;
  responseStatus?: number;
  errorKind?: string;
  errorSnippet?: string;
  durationMs: number;
}

export interface BatchResult {
  index: number;
  task?: Task;
  error?: { code: string; detail: string };
}

export interface ListTasksParams {
  state?: "pending" | "running" | "succeeded" | "failed";
  idPrefix?: string;
  view?: "basic" | "full";
  pageSize?: number;
  pageToken?: string;
}

export interface ListQueuesParams {
  namePrefix?: string;
  pageSize?: number;
  pageToken?: string;
}

export interface Page<T> {
  items: T[];
  nextPageToken?: string;
}
