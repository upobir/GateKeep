-- GateKeep application data.
-- A tool call stays in tool_calls for its whole life. Pending rows are the
-- approval queue. Finished rows are history.

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE resources (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  execution_timeout_ms INTEGER,
  cache_idle_ms INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE tool_calls (
  id INTEGER PRIMARY KEY,
  resource_id INTEGER REFERENCES resources(id) ON DELETE SET NULL,
  resource_name TEXT NOT NULL,
  harness_name TEXT NOT NULL,
  command TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL,
  decision TEXT,
  decision_reason TEXT,
  execution_status TEXT,
  result TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  decided_at TEXT,
  finished_at TEXT
);

CREATE INDEX tool_calls_status_created_at ON tool_calls(status, created_at);
CREATE INDEX tool_calls_created_at ON tool_calls(created_at);
CREATE INDEX tool_calls_finished_at ON tool_calls(finished_at, id);

-- One active password, plus at most one pending generation while a password
-- change copies secrets. The verifier is an AES-GCM ciphertext of random
-- bytes, sealed with a key derived from the user's password.

CREATE TABLE vault (
  generation INTEGER PRIMARY KEY,
  state TEXT NOT NULL CHECK (state IN ('active', 'pending')),
  salt BLOB NOT NULL,
  time INTEGER NOT NULL,
  memory INTEGER NOT NULL,
  threads INTEGER NOT NULL,
  verifier BLOB NOT NULL,
  created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX vault_one_active ON vault(state) WHERE state = 'active';
CREATE UNIQUE INDEX vault_one_pending ON vault(state) WHERE state = 'pending';

CREATE TABLE resource_secrets (
  resource_id INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  generation INTEGER NOT NULL,
  ciphertext BLOB NOT NULL,
  PRIMARY KEY (resource_id, generation)
);
