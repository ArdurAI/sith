-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE sith.oidc_bindings (
    workspace_id text NOT NULL,
    issuer text NOT NULL,
    upstream_subject text NOT NULL,
    member_subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (workspace_id, issuer, upstream_subject),
    FOREIGN KEY (workspace_id, member_subject)
        REFERENCES sith.memberships(workspace_id, subject) ON DELETE CASCADE,
    CONSTRAINT oidc_bindings_issuer_valid CHECK (
        issuer = btrim(issuer) AND issuer LIKE 'https://%' AND octet_length(issuer) <= 2048
        AND issuer !~ '[[:cntrl:]]'
    ),
    CONSTRAINT oidc_bindings_upstream_subject_valid CHECK (
        upstream_subject = btrim(upstream_subject) AND upstream_subject <> ''
        AND octet_length(upstream_subject) <= 256 AND upstream_subject !~ '[[:cntrl:]]'
    )
);
CREATE INDEX oidc_bindings_member_idx
    ON sith.oidc_bindings (workspace_id, member_subject);

ALTER TABLE sith.oidc_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.oidc_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.oidc_bindings
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
