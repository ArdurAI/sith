-- SPDX-License-Identifier: Apache-2.0

-- Exact immutable image CVE lookup is constrained to the normalized, report-derived image
-- payload. The index deliberately excludes raw report fields and all non-CVE facts.
CREATE INDEX fleet_facts_cve_image_idx
    ON sith.fleet_facts (workspace_id, (payload ->> 'image'), cluster_id, observed_at DESC)
    WHERE kind = 'cve';
