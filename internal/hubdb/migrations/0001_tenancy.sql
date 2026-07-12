-- SPDX-License-Identifier: Apache-2.0

CREATE SCHEMA sith;
REVOKE ALL ON SCHEMA sith FROM PUBLIC;

CREATE TABLE sith.workspaces (
    id text PRIMARY KEY,
    name text NOT NULL,
    tenant_key text NOT NULL,
    CONSTRAINT workspaces_id_valid CHECK (
        id = btrim(id) AND id <> '' AND octet_length(id) <= 256 AND id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT workspaces_name_valid CHECK (
        name = btrim(name) AND name <> '' AND octet_length(name) <= 256 AND name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT workspaces_tenant_key_valid CHECK (
        tenant_key = btrim(tenant_key) AND tenant_key <> '' AND octet_length(tenant_key) <= 256
        AND tenant_key !~ '[[:cntrl:]]'
    )
);

CREATE TABLE sith.memberships (
    workspace_id text NOT NULL REFERENCES sith.workspaces(id) ON DELETE CASCADE,
    subject text NOT NULL,
    role text NOT NULL,
    PRIMARY KEY (workspace_id, subject),
    CONSTRAINT memberships_subject_valid CHECK (
        subject = btrim(subject) AND subject <> '' AND octet_length(subject) <= 256
        AND subject !~ '[[:cntrl:]]'
    ),
    CONSTRAINT memberships_role_valid CHECK (role IN ('reader', 'operator', 'approver', 'admin'))
);
CREATE INDEX memberships_subject_idx ON sith.memberships (subject, workspace_id);

CREATE TABLE sith.clusters (
    workspace_id text NOT NULL REFERENCES sith.workspaces(id) ON DELETE CASCADE,
    id text NOT NULL,
    managed_cluster_ref text NOT NULL,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_seen timestamptz,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, managed_cluster_ref),
    CONSTRAINT clusters_id_valid CHECK (
        id = btrim(id) AND id <> '' AND octet_length(id) <= 256 AND id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT clusters_ref_valid CHECK (
        managed_cluster_ref = btrim(managed_cluster_ref) AND managed_cluster_ref <> ''
        AND octet_length(managed_cluster_ref) <= 1024 AND managed_cluster_ref !~ '[[:cntrl:]]'
    ),
    CONSTRAINT clusters_labels_object CHECK (jsonb_typeof(labels) = 'object')
);

CREATE TABLE sith.fleet_facts (
    workspace_id text NOT NULL,
    id bigint GENERATED ALWAYS AS IDENTITY,
    cluster_id text NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, cluster_id)
        REFERENCES sith.clusters(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT fleet_facts_kind_valid CHECK (
        kind = btrim(kind) AND kind <> '' AND octet_length(kind) <= 128 AND kind !~ '[[:cntrl:]]'
    )
);
CREATE INDEX fleet_facts_cluster_observed_idx
    ON sith.fleet_facts (workspace_id, cluster_id, observed_at DESC);

ALTER TABLE sith.workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.workspaces FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.workspaces
    FOR ALL TO PUBLIC
    USING (id = current_setting('sith.workspace_id', true))
    WITH CHECK (id = current_setting('sith.workspace_id', true));

ALTER TABLE sith.memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.memberships
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));

ALTER TABLE sith.clusters ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.clusters FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.clusters
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));

ALTER TABLE sith.fleet_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.fleet_facts FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.fleet_facts
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
