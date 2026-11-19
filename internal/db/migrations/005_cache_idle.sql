-- How long a resource cache may sit unused before it is dropped.
-- Null uses the application default.

ALTER TABLE resources ADD COLUMN cache_idle_ms INTEGER;
