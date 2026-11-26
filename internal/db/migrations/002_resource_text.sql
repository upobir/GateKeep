-- Add MCP context text and scope connection names to their resource type.
CREATE TABLE resources_new (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  resource_text TEXT NOT NULL DEFAULT '',
  execution_timeout_ms INTEGER,
  cache_idle_ms INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (type, name)
);
INSERT INTO resources_new (id, name, type, config_json, resource_text, execution_timeout_ms, cache_idle_ms, created_at, updated_at)
SELECT id, name, type, config_json, '', execution_timeout_ms, cache_idle_ms, created_at, updated_at FROM resources;
DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
