-- Dashboard accounts, login sessions, team invites and dashboard-managed API
-- keys. Timestamps are unix milliseconds. Secrets (session tokens, invite
-- tokens, API keys) are stored only as SHA-256 hex digests.

CREATE TABLE users (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  email          TEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL DEFAULT '',
  password_hash  TEXT NOT NULL,
  role           TEXT NOT NULL CHECK (role IN ('admin','member','viewer')),
  created_at     BIGINT NOT NULL,
  updated_at     BIGINT NOT NULL,
  last_login_at  BIGINT
);

CREATE TABLE sessions (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  token_hash  TEXT NOT NULL UNIQUE,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  BIGINT NOT NULL,
  expires_at  BIGINT NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE invites (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  token_hash  TEXT NOT NULL UNIQUE,
  kind        TEXT NOT NULL CHECK (kind IN ('invite','reset')),
  email       TEXT NOT NULL,
  role        TEXT CHECK (role IN ('admin','member','viewer')),
  user_id     BIGINT REFERENCES users(id) ON DELETE CASCADE,
  created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at  BIGINT NOT NULL,
  expires_at  BIGINT NOT NULL
);

CREATE TABLE api_keys (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name          TEXT NOT NULL,
  prefix        TEXT NOT NULL,
  key_hash      TEXT NOT NULL UNIQUE,
  role          TEXT NOT NULL CHECK (role IN ('admin','writer')),
  created_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at    BIGINT NOT NULL,
  last_used_at  BIGINT
);
