-- SPDX-License-Identifier: Apache-2.0

-- Exact immutable runtime image lookup is constrained to normalized inventory facts. The
-- expression index supports JSONB array-element membership without indexing raw Pod objects.
CREATE INDEX fleet_facts_inventory_image_digests_idx
    ON sith.fleet_facts USING GIN ((payload -> 'image_digests'))
    WHERE kind = 'inventory';
