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
  secrets_ciphertext BLOB,
  execution_timeout_ms INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE harnesses (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE harness_tokens (
  id INTEGER PRIMARY KEY,
  harness_id INTEGER NOT NULL REFERENCES harnesses(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE tool_calls (
  id INTEGER PRIMARY KEY,
  resource_id INTEGER REFERENCES resources(id) ON DELETE SET NULL,
  resource_name TEXT NOT NULL,
  harness_id INTEGER REFERENCES harnesses(id) ON DELETE SET NULL,
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
