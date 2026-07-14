-- SPDX-License-Identifier: Apache-2.0

-- Exact CVE identifier lookup is restricted to the normalized identifier array on CVE facts.
-- The partial expression index cannot contain inventory or raw report data.
CREATE INDEX fleet_facts_cve_identifier_idx
    ON sith.fleet_facts USING GIN ((payload -> 'ids'))
    WHERE kind = 'cve';
