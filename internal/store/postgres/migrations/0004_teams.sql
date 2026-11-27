-- SaaS mode (WorkOS configured): teams are WorkOS Organizations. Each team's
-- queues live in a private namespace: queue names are stored as "<ns>:<name>"
-- (":" can't appear in a queue name), so teams can reuse names and never see
-- each other's data. Self-hosted mode doesn't use these tables.

CREATE TABLE teams (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ns             TEXT NOT NULL UNIQUE,      -- random, never reused
  name           TEXT NOT NULL,
  workos_org_id  TEXT UNIQUE,
  created_at     BIGINT NOT NULL,
  updated_at     BIGINT NOT NULL
);

CREATE TABLE team_members (
  team_id               BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role                  TEXT NOT NULL CHECK (role IN ('admin','member','viewer')),
  workos_membership_id  TEXT UNIQUE,
  created_at            BIGINT NOT NULL,
  PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user ON team_members (user_id);

-- Role to grant when an invited person joins (WorkOS sends the invite email).
CREATE TABLE team_invites (
  id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  team_id               BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  email                 TEXT NOT NULL,
  role                  TEXT NOT NULL CHECK (role IN ('admin','member','viewer')),
  workos_invitation_id  TEXT UNIQUE,
  created_at            BIGINT NOT NULL,
  expires_at            BIGINT NOT NULL
);
CREATE INDEX team_invites_team_email ON team_invites (team_id, email);

ALTER TABLE sessions ADD COLUMN team_id BIGINT REFERENCES teams(id) ON DELETE SET NULL;
ALTER TABLE api_keys ADD COLUMN team_id BIGINT REFERENCES teams(id) ON DELETE CASCADE;
ALTER TABLE users ADD COLUMN last_team_id BIGINT REFERENCES teams(id) ON DELETE SET NULL;
