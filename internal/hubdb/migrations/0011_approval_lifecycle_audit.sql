-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE sith.policy_audit_entries
    ADD COLUMN event_kind text,
    ADD COLUMN evidence_digest text;

UPDATE sith.policy_audit_entries
SET event_kind = 'policy-decision', evidence_digest = '';

ALTER TABLE sith.policy_audit_entries
	ALTER COLUMN event_kind SET NOT NULL,
	ALTER COLUMN event_kind SET DEFAULT 'policy-decision',
	ALTER COLUMN evidence_digest SET NOT NULL,
	ALTER COLUMN evidence_digest SET DEFAULT '',
    DROP CONSTRAINT policy_audit_entries_format_valid,
    DROP CONSTRAINT policy_audit_entries_action_valid,
    ADD CONSTRAINT policy_audit_entries_format_valid CHECK (format_version IN (1, 2)),
    ADD CONSTRAINT policy_audit_entries_action_valid CHECK (
        action IN ('read', 'propose-intent', 'approve-intent')
    ),
    ADD CONSTRAINT policy_audit_entries_kind_valid CHECK (
        event_kind IN ('policy-decision', 'approval-created', 'approval-consumed')
    ),
	ADD CONSTRAINT policy_audit_entries_evidence_valid CHECK (
		(format_version = 1 AND event_kind = 'policy-decision' AND evidence_digest = '')
		OR
		(format_version = 2 AND event_kind IN ('approval-created', 'approval-consumed')
		 AND evidence_digest ~ '^sha256:[0-9a-f]{64}$')
	),
	ADD CONSTRAINT policy_audit_entries_lifecycle_shape_valid CHECK (
		format_version = 1
		OR
		(format_version = 2 AND verb = 'approval.grant' AND verdict = 'allow'
		 AND reason_code = event_kind
		 AND (
			(event_kind = 'approval-created' AND role = 'approver' AND action = 'approve-intent')
			OR
			(event_kind = 'approval-consumed' AND role = 'operator' AND action = 'propose-intent')
		 ))
	);

-- Defaults let pre-0011 writers continue appending format-1 decisions while the schema rolls
-- forward. Upgrade every verifier before enabling format-2 approval lifecycle writers.
