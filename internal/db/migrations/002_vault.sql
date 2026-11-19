-- One row. The verifier is an AES-GCM ciphertext of random bytes, sealed
-- with a key derived from the user's password.

CREATE TABLE vault (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  salt BLOB NOT NULL,
  time INTEGER NOT NULL,
  memory INTEGER NOT NULL,
  threads INTEGER NOT NULL,
  verifier BLOB NOT NULL,
  created_at TEXT NOT NULL
);
