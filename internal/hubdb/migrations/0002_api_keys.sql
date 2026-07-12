-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE sith.api_keys (
    workspace_id text NOT NULL,
    id text NOT NULL,
    subject text NOT NULL,
    verifier bytea NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    retire_at timestamptz,
    revoked_at timestamptz,
    replaced_by text,
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, subject)
        REFERENCES sith.memberships(workspace_id, subject) ON DELETE CASCADE,
    CONSTRAINT api_keys_id_valid CHECK (
        id ~ '^[A-Za-z0-9_-]{22}$'
    ),
    CONSTRAINT api_keys_verifier_valid CHECK (octet_length(verifier) = 32),
    CONSTRAINT api_keys_lifetime_valid CHECK (expires_at > created_at),
    CONSTRAINT api_keys_retirement_valid CHECK (retire_at IS NULL OR retire_at >= created_at),
    CONSTRAINT api_keys_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT api_keys_replacement_valid CHECK (
        replaced_by IS NULL OR replaced_by ~ '^[A-Za-z0-9_-]{22}$'
    )
);
CREATE INDEX api_keys_subject_idx ON sith.api_keys (workspace_id, subject);
CREATE INDEX api_keys_expiry_idx ON sith.api_keys (expires_at);

ALTER TABLE sith.api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.api_keys
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
