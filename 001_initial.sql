CREATE TABLE workspace (
    id integer PRIMARY KEY CHECK (id = 1),
    version bigint NOT NULL
);
INSERT INTO workspace (id, version) VALUES (1, 0);

CREATE TABLE users (
    id text PRIMARY KEY,
    position integer NOT NULL,
    name text NOT NULL,
    username text NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'member')),
    disabled boolean NOT NULL,
    must_change_password boolean NOT NULL,
    salt text NOT NULL,
    password_hash text NOT NULL,
    -- Restores may swap usernames or replace account IDs within one transaction.
    CONSTRAINT users_username_key UNIQUE (username) DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE teams (
    id text PRIMARY KEY,
    position integer NOT NULL,
    version bigint NOT NULL,
    name text NOT NULL,
    stage_minutes jsonb NOT NULL
);

CREATE TABLE team_members (
    team_id text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id),
    position integer NOT NULL,
    PRIMARY KEY (team_id, user_id)
);

-- Each work item has its own row. JSONB preserves nested values, assignee
-- arrays, and immutable meeting snapshots without changing the backup format.
CREATE TABLE team_items (
    team_id text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('metrics', 'rocks', 'todos', 'issues', 'headlines', 'meetings')),
    id text NOT NULL,
    position integer NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND payload ? 'id' AND payload->>'id' = id),
    PRIMARY KEY (team_id, id)
);
CREATE INDEX team_items_order ON team_items(team_id, kind, position);