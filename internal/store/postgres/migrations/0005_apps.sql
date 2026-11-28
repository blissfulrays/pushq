-- Applications: each team (or, self-hosted, the whole server) has one or
-- more applications, and every queue belongs to exactly one. An application
-- is a queue namespace like a team's: stored queue names are "<ns>:<name>".
--
-- The Default application reuses the namespace queues already had (the
-- team's ns in SaaS mode, "" self-hosted), so existing queues, API keys and
-- clients keep working unchanged. Other applications get a random ns.
-- team_id is NULL for the self-hosted (unteamed) applications.

CREATE TABLE apps (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  team_id     BIGINT REFERENCES teams(id) ON DELETE CASCADE,
  ns          TEXT NOT NULL,
  name        TEXT NOT NULL,
  is_default  INTEGER NOT NULL DEFAULT 0,
  created_at  BIGINT NOT NULL,
  updated_at  BIGINT NOT NULL
);
-- Namespaces are never shared: a team's default app holds the team's ns, so
-- this also keeps a new app's ns from ever matching a team's.
CREATE UNIQUE INDEX apps_ns           ON apps (ns);
CREATE UNIQUE INDEX apps_team_name    ON apps (COALESCE(team_id, 0), lower(name));
CREATE UNIQUE INDEX apps_team_default ON apps (COALESCE(team_id, 0)) WHERE is_default = 1;

INSERT INTO apps (team_id, ns, name, is_default, created_at, updated_at)
  SELECT id, ns, 'Default', 1, created_at, created_at FROM teams;
INSERT INTO apps (team_id, ns, name, is_default, created_at, updated_at)
  VALUES (NULL, '', 'Default', 1, (EXTRACT(EPOCH FROM now()) * 1000)::BIGINT, (EXTRACT(EPOCH FROM now()) * 1000)::BIGINT);

-- A key with app_id set can reach only that application.
ALTER TABLE api_keys ADD COLUMN app_id BIGINT REFERENCES apps(id) ON DELETE CASCADE;
