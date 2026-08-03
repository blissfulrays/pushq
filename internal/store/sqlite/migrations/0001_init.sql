CREATE TABLE queues (
  id                        INTEGER PRIMARY KEY,
  name                      TEXT NOT NULL UNIQUE,
  state                     TEXT NOT NULL DEFAULT 'running'
                            CHECK (state IN ('running','paused','disabled')),
  max_dispatches_per_second REAL NOT NULL DEFAULT 500,
  max_burst_size            INTEGER,
  max_concurrent_dispatches INTEGER NOT NULL DEFAULT 1000,
  retry_max_attempts        INTEGER NOT NULL DEFAULT 100,
  retry_max_duration_ms     INTEGER NOT NULL DEFAULT 0,
  retry_min_backoff_ms      INTEGER NOT NULL DEFAULT 100,
  retry_max_backoff_ms      INTEGER NOT NULL DEFAULT 3600000,
  retry_max_doublings       INTEGER NOT NULL DEFAULT 16,
  target_base_url           TEXT,
  target_method             TEXT,
  target_headers            TEXT,
  signing_secret            TEXT,
  dead_letter_queue         TEXT,
  purged_at                 INTEGER,
  config_version            INTEGER NOT NULL DEFAULT 1,
  created_at                INTEGER NOT NULL,
  updated_at                INTEGER NOT NULL
);

CREATE TABLE tasks (
  id                   INTEGER PRIMARY KEY,
  queue_id             INTEGER NOT NULL REFERENCES queues(id) ON DELETE CASCADE,
  task_id              TEXT NOT NULL,
  state                TEXT NOT NULL DEFAULT 'pending'
                       CHECK (state IN ('pending','leased','succeeded','failed','deleted')),
  url                  TEXT NOT NULL,
  method               TEXT NOT NULL DEFAULT 'POST',
  headers              TEXT,
  body                 BLOB,
  dispatch_deadline_ms INTEGER NOT NULL DEFAULT 600000,
  retry_override       TEXT,
  schedule_time        INTEGER NOT NULL,
  created_at           INTEGER NOT NULL,
  first_attempt_at     INTEGER,
  finished_at          INTEGER,
  dispatch_count       INTEGER NOT NULL DEFAULT 0,
  response_count       INTEGER NOT NULL DEFAULT 0,
  lease_token          TEXT,
  lease_expires_at     INTEGER,
  last_attempt_at      INTEGER,
  last_status          INTEGER,
  last_error           TEXT,
  UNIQUE (queue_id, task_id)
);

CREATE INDEX tasks_claim   ON tasks (queue_id, schedule_time) WHERE state = 'pending';
CREATE INDEX tasks_leases  ON tasks (lease_expires_at)        WHERE state = 'leased';
CREATE INDEX tasks_cleanup ON tasks (finished_at)             WHERE state IN ('succeeded','failed','deleted');
CREATE INDEX tasks_list    ON tasks (queue_id, state, id DESC);

CREATE TABLE task_attempts (
  id              INTEGER PRIMARY KEY,
  task_pk         INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  queue_id        INTEGER NOT NULL,
  attempt_number  INTEGER NOT NULL,
  dispatch_time   INTEGER NOT NULL,
  response_time   INTEGER,
  response_status INTEGER,
  error_kind      TEXT,
  error_snippet   TEXT,
  duration_ms     INTEGER
);

CREATE INDEX attempts_by_task ON task_attempts (task_pk, attempt_number);
CREATE INDEX attempts_stats   ON task_attempts (queue_id, dispatch_time DESC);
