-- Links a dashboard account to its WorkOS user, so "Sign in with WorkOS"
-- matches people by a stable ID rather than by email (which can change).
ALTER TABLE users ADD COLUMN workos_user_id TEXT;
CREATE UNIQUE INDEX users_workos_user_id ON users (workos_user_id) WHERE workos_user_id IS NOT NULL;
