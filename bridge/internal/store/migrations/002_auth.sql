CREATE TABLE IF NOT EXISTS account_password_credentials (
  account_id   TEXT PRIMARY KEY REFERENCES accounts(id),
  password_hash TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS external_identities (
  id                   TEXT PRIMARY KEY,
  account_id           TEXT NOT NULL REFERENCES accounts(id),
  identity_provider_id TEXT NOT NULL REFERENCES identity_providers(id),
  subject              TEXT NOT NULL,
  email_snapshot       TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL,
  UNIQUE (identity_provider_id, subject)
);

CREATE TABLE IF NOT EXISTS sessions (
  id             TEXT PRIMARY KEY,
  account_id     TEXT NOT NULL REFERENCES accounts(id),
  token_hash     TEXT NOT NULL UNIQUE,
  csrf_token_hash TEXT NOT NULL,
  expires_at     TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  revoked_at     TEXT
);

CREATE INDEX IF NOT EXISTS sessions_account_idx ON sessions(account_id, expires_at);

CREATE TABLE IF NOT EXISTS oauth_authorization_states (
  id                   TEXT PRIMARY KEY,
  identity_provider_id TEXT NOT NULL REFERENCES identity_providers(id),
  state_hash           TEXT NOT NULL UNIQUE,
  nonce                TEXT NOT NULL,
  code_verifier_ciphertext BLOB NOT NULL,
  code_verifier_nonce  BLOB NOT NULL,
  redirect_uri         TEXT NOT NULL,
  expires_at           TEXT NOT NULL,
  consumed_at          TEXT
);
