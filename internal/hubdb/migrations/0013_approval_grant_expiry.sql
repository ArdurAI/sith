-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE sith.approval_grants
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN evidence_version smallint;

-- Existing rows retain their historical format-2 evidence. They receive a forensic expiry value,
-- but the format marker keeps every legacy unconsumed grant outside the new consumption predicate.
-- ApplyMigrations holds one serializable transaction; ALTER takes an access-exclusive lock, and a
-- failure rolls this owner-only FORCE-RLS relaxation back before any application can observe it.
ALTER TABLE sith.approval_grants NO FORCE ROW LEVEL SECURITY;
UPDATE sith.approval_grants
SET expires_at = approved_at + interval '10 minutes', evidence_version = 1;
ALTER TABLE sith.approval_grants FORCE ROW LEVEL SECURITY;

ALTER TABLE sith.approval_grants
    ALTER COLUMN expires_at SET NOT NULL,
    ALTER COLUMN evidence_version SET NOT NULL,
    ADD CONSTRAINT approval_grants_expiry_valid CHECK (
        expires_at = approved_at + interval '10 minutes'
    ),
    ADD CONSTRAINT approval_grants_evidence_version_valid CHECK (evidence_version IN (1, 2)),
    ADD CONSTRAINT approval_grants_v2_consumption_window_valid CHECK (
        evidence_version = 1 OR consumed_at IS NULL OR consumed_at < expires_at
    );

ALTER TABLE sith.policy_audit_entries
    DROP CONSTRAINT policy_audit_entries_format_valid,
    DROP CONSTRAINT policy_audit_entries_evidence_valid,
    DROP CONSTRAINT policy_audit_entries_lifecycle_shape_valid,
    ADD CONSTRAINT policy_audit_entries_format_valid CHECK (format_version IN (1, 2, 3)),
    ADD CONSTRAINT policy_audit_entries_evidence_valid CHECK (
        (format_version = 1 AND event_kind = 'policy-decision' AND evidence_digest = '')
        OR
        (format_version IN (2, 3) AND event_kind IN ('approval-created', 'approval-consumed')
         AND evidence_digest ~ '^sha256:[0-9a-f]{64}$')
    ),
    ADD CONSTRAINT policy_audit_entries_lifecycle_shape_valid CHECK (
        format_version = 1
        OR
        (format_version IN (2, 3) AND verb = 'approval.grant' AND verdict = 'allow'
         AND reason_code = event_kind
         AND (
            (event_kind = 'approval-created' AND role = 'approver' AND action = 'approve-intent')
            OR
            (event_kind = 'approval-consumed' AND role = 'operator' AND action = 'propose-intent')
         ))
    );
