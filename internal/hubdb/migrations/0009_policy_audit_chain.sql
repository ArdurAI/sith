-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE sith.policy_audit_heads (
    workspace_id text PRIMARY KEY REFERENCES sith.workspaces(id) ON DELETE RESTRICT,
    last_sequence bigint NOT NULL DEFAULT 0,
    last_hash bytea NOT NULL DEFAULT decode(repeat('00', 32), 'hex'),
    CONSTRAINT policy_audit_heads_sequence_valid CHECK (last_sequence >= 0),
    CONSTRAINT policy_audit_heads_hash_valid CHECK (octet_length(last_hash) = 32)
);

CREATE TABLE sith.policy_audit_entries (
    workspace_id text NOT NULL REFERENCES sith.policy_audit_heads(workspace_id) ON DELETE RESTRICT,
    sequence bigint NOT NULL,
    format_version smallint NOT NULL,
    recorded_at timestamptz NOT NULL,
    trace_id text NOT NULL,
    actor text NOT NULL,
    role text NOT NULL,
    action text NOT NULL,
    verb text NOT NULL,
    verdict text NOT NULL,
    reason_code text NOT NULL,
    previous_hash bytea NOT NULL,
    entry_hash bytea NOT NULL,
    PRIMARY KEY (workspace_id, sequence),
    CONSTRAINT policy_audit_entries_sequence_valid CHECK (sequence > 0),
    CONSTRAINT policy_audit_entries_format_valid CHECK (format_version = 1),
    CONSTRAINT policy_audit_entries_trace_valid CHECK (trace_id ~ '^[0-9a-f]{32}$'),
    CONSTRAINT policy_audit_entries_actor_valid CHECK (
        actor = btrim(actor) AND actor <> '' AND octet_length(actor) <= 256 AND actor !~ '[[:cntrl:]]'
    ),
    CONSTRAINT policy_audit_entries_role_valid CHECK (role IN ('reader', 'operator', 'approver', 'admin')),
    CONSTRAINT policy_audit_entries_action_valid CHECK (action IN ('read', 'propose-intent')),
    CONSTRAINT policy_audit_entries_verb_valid CHECK (
        verb = btrim(verb) AND verb <> '' AND octet_length(verb) <= 128 AND verb !~ '[[:cntrl:]]'
    ),
    CONSTRAINT policy_audit_entries_verdict_valid CHECK (verdict IN ('allow', 'deny', 'require-approval')),
    CONSTRAINT policy_audit_entries_reason_valid CHECK (
        reason_code ~ '^[a-z0-9_.-]+$' AND octet_length(reason_code) <= 64
    ),
    CONSTRAINT policy_audit_entries_previous_hash_valid CHECK (octet_length(previous_hash) = 32),
    CONSTRAINT policy_audit_entries_entry_hash_valid CHECK (octet_length(entry_hash) = 32)
);

ALTER TABLE sith.policy_audit_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.policy_audit_heads FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.policy_audit_heads
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));

ALTER TABLE sith.policy_audit_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE sith.policy_audit_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON sith.policy_audit_entries
    FOR ALL TO PUBLIC
    USING (workspace_id = current_setting('sith.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('sith.workspace_id', true));
