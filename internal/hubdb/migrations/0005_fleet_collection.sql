-- SPDX-License-Identifier: Apache-2.0

-- Collection status is deliberately closed and carries no transport error text, endpoint, token,
-- or kubeconfig material. A failed attempt retains the last normalized snapshot as stale evidence.
ALTER TABLE sith.clusters
    ADD COLUMN last_attempted_at timestamptz,
    ADD COLUMN last_error_kind text,
    ADD CONSTRAINT clusters_last_error_kind_valid CHECK (
        last_error_kind IS NULL OR last_error_kind IN ('transport', 'deadline', 'invalid-snapshot')
    );

-- Preserve structured fleet identity/provenance beside the normalized observation payload. Defaults
-- make this compatible with the tenancy seed rows that predate read-federation collection.
ALTER TABLE sith.fleet_facts
    ADD COLUMN resource_ref jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN source text NOT NULL DEFAULT '',
    ADD COLUMN provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN display jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT fleet_facts_resource_ref_object CHECK (jsonb_typeof(resource_ref) = 'object'),
    ADD CONSTRAINT fleet_facts_source_valid CHECK (
        source = btrim(source) AND octet_length(source) <= 256 AND source !~ '[[:cntrl:]]'
    ),
    ADD CONSTRAINT fleet_facts_provenance_object CHECK (jsonb_typeof(provenance) = 'object'),
    ADD CONSTRAINT fleet_facts_display_array CHECK (jsonb_typeof(display) = 'array');

CREATE INDEX fleet_facts_workspace_cluster_kind_observed_idx
    ON sith.fleet_facts (workspace_id, cluster_id, kind, observed_at DESC);
