CREATE TABLE queues (
  id                        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name                      TEXT NOT NULL UNIQUE,
  state                     TEXT NOT NULL DEFAULT 'running'
                            CHECK (state IN ('running','paused','disabled')),
  max_dispatches_per_second DOUBLE PRECISION NOT NULL DEFAULT 500,
  max_burst_size            INT,
  max_concurrent_dispatches INT NOT NULL DEFAULT 1000,
  retry_max_attempts        INT NOT NULL DEFAULT 100,
  retry_max_duration_ms     BIGINT NOT NULL DEFAULT 0,
  retry_min_backoff_ms      BIGINT NOT NULL DEFAULT 100,
  retry_max_backoff_ms      BIGINT NOT NULL DEFAULT 3600000,
  retry_max_doublings       INT NOT NULL DEFAULT 16,
  target_base_url           TEXT,
  target_method             TEXT,
  target_headers            JSONB,
  signing_secret            TEXT,
  dead_letter_queue         TEXT,
  purged_at                 TIMESTAMPTZ,
  config_version            BIGINT NOT NULL DEFAULT 1,
  created_at                TIMESTAMPTZ NOT NULL,
  updated_at                TIMESTAMPTZ NOT NULL
);

CREATE TABLE tasks (
  id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  queue_id             BIGINT NOT NULL REFERENCES queues(id) ON DELETE CASCADE,
  task_id              TEXT NOT NULL,
  state                TEXT NOT NULL DEFAULT 'pending'
                       CHECK (state IN ('pending','leased','succeeded','failed','deleted')),
  url                  TEXT NOT NULL,
  method               TEXT NOT NULL DEFAULT 'POST',
  headers              JSONB,
  body                 BYTEA,
  dispatch_deadline_ms BIGINT NOT NULL DEFAULT 600000,
  retry_override       JSONB,
  schedule_time        TIMESTAMPTZ NOT NULL,
  created_at           TIMESTAMPTZ NOT NULL,
  first_attempt_at     TIMESTAMPTZ,
  finished_at          TIMESTAMPTZ,
  dispatch_count       INT NOT NULL DEFAULT 0,
  response_count       INT NOT NULL DEFAULT 0,
  lease_token          TEXT,
  lease_expires_at     TIMESTAMPTZ,
  last_attempt_at      TIMESTAMPTZ,
  last_status          INT,
  last_error           TEXT,
  UNIQUE (queue_id, task_id)
);

CREATE INDEX tasks_claim   ON tasks (queue_id, schedule_time) WHERE state = 'pending';
CREATE INDEX tasks_leases  ON tasks (lease_expires_at)        WHERE state = 'leased';
CREATE INDEX tasks_cleanup ON tasks (finished_at)             WHERE state IN ('succeeded','failed','deleted');
CREATE INDEX tasks_list    ON tasks (queue_id, state, id DESC);

CREATE TABLE task_attempts (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  task_pk         BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  queue_id        BIGINT NOT NULL,
  attempt_number  INT NOT NULL,
  dispatch_time   TIMESTAMPTZ NOT NULL,
  response_time   TIMESTAMPTZ,
  response_status INT,
  error_kind      TEXT,
  error_snippet   TEXT,
  duration_ms     BIGINT
);

CREATE INDEX attempts_by_task ON task_attempts (task_pk, attempt_number);
CREATE INDEX attempts_stats   ON task_attempts (queue_id, dispatch_time DESC);
