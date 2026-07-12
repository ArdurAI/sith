-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE sith.cloud_identity_bindings (
    workspace_id text NOT NULL,
    provider text NOT NULL,
    realm text NOT NULL,
    upstream_subject text NOT NULL,
    member_subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (workspace_id, provider, realm, upstream_subject),
    FOREIGN KEY (workspace_id, member_subject)
        REFERENCES sith.memberships(workspace_id, subject) ON DELETE CASCADE,
    CONSTRAINT cloud_identity_bindings_provider_valid CHECK (provider IN ('aws', 'azure', 'gcp')),
    CONSTRAINT cloud_identity_bindings_realm_valid CHECK (
        realm = btrim(realm) AND realm <> '' AND octet_length(realm) <= 256 AND realm !~ '[[:cntrl:]]'
    ),
    CONSTRAINT cloud_identity_bindings_subject_valid CHECK (
        upstream_subject = btrim(upstream_subject) AND upstream_subject <> ''
        AND octet_length(upstream_subject) <= 256 AND upstream_subject !~ '[[:cntrl:]]'
    )
);
CREATE INDEX cloud_identity_bindings_member_idx
    ON sith.cloud_identity_bindings (workspace_id, member_subject);

ALTER TABLE sith.cloud_identity_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.cloud_identity_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.cloud_identity_bindings
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
