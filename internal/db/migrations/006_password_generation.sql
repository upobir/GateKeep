-- A password change writes a new vault generation and new secret copies,
-- then deletes the previous generation. A crash before that switch leaves
-- the active generation untouched.

CREATE TABLE vault_next (
  generation INTEGER PRIMARY KEY,
  state TEXT NOT NULL CHECK (state IN ('active', 'pending')),
  salt BLOB NOT NULL,
  time INTEGER NOT NULL,
  memory INTEGER NOT NULL,
  threads INTEGER NOT NULL,
  verifier BLOB NOT NULL,
  created_at TEXT NOT NULL
);

INSERT INTO vault_next (generation, state, salt, time, memory, threads, verifier, created_at)
SELECT 1, 'active', salt, time, memory, threads, verifier, created_at FROM vault;

DROP TABLE vault;

ALTER TABLE vault_next RENAME TO vault;

CREATE UNIQUE INDEX vault_one_active ON vault(state) WHERE state = 'active';

CREATE UNIQUE INDEX vault_one_pending ON vault(state) WHERE state = 'pending';

CREATE TABLE resource_secrets (
  resource_id INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  generation INTEGER NOT NULL,
  ciphertext BLOB NOT NULL,
  PRIMARY KEY (resource_id, generation)
);

INSERT INTO resource_secrets (resource_id, generation, ciphertext)
SELECT id, 1, secrets_ciphertext FROM resources
WHERE secrets_ciphertext IS NOT NULL AND length(secrets_ciphertext) > 0;

UPDATE resources SET secrets_ciphertext = NULL;
