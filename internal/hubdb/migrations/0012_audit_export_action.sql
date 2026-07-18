-- SPDX-License-Identifier: Apache-2.0

ALTER TABLE sith.policy_audit_entries
    DROP CONSTRAINT policy_audit_entries_action_valid,
    ADD CONSTRAINT policy_audit_entries_action_valid CHECK (
        action IN ('read', 'export-audit', 'propose-intent', 'approve-intent')
    );
