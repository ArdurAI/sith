-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE sith.approval_grants (
    workspace_id text NOT NULL REFERENCES sith.workspaces(id) ON DELETE CASCADE,
    id text NOT NULL,
    intent_id text NOT NULL,
    proposer text NOT NULL,
    approver text NOT NULL,
    resolved_digest text NOT NULL,
    approved_at timestamptz NOT NULL,
    consumed_at timestamptz,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, intent_id, approver, resolved_digest),
    CONSTRAINT approval_grants_id_valid CHECK (id ~ '^[A-Za-z0-9_-]{22}$'),
    CONSTRAINT approval_grants_intent_valid CHECK (
        intent_id = btrim(intent_id) AND intent_id <> '' AND octet_length(intent_id) <= 253
        AND intent_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT approval_grants_proposer_valid CHECK (
        proposer = btrim(proposer) AND proposer <> '' AND octet_length(proposer) <= 256
        AND proposer !~ '[[:cntrl:]]'
    ),
    CONSTRAINT approval_grants_approver_valid CHECK (
        approver = btrim(approver) AND approver <> '' AND octet_length(approver) <= 256
        AND approver !~ '[[:cntrl:]]'
    ),
    CONSTRAINT approval_grants_separation_of_duty CHECK (proposer <> approver),
    CONSTRAINT approval_grants_digest_valid CHECK (resolved_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT approval_grants_consumption_valid CHECK (consumed_at IS NULL OR consumed_at >= approved_at)
);
CREATE INDEX approval_grants_pending_intent_idx
    ON sith.approval_grants (workspace_id, intent_id, resolved_digest)
    WHERE consumed_at IS NULL;

ALTER TABLE sith.approval_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.approval_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.approval_grants
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
