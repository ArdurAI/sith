# Deterministic incident replays

These fixtures are deliberately synthetic and sanitized. They model only normalized brain input;
they must not contain real cluster names, endpoint URLs, credentials, secrets, raw telemetry, or
customer incident data. The test-only harness rejects unknown JSON fields so every expected verdict
shape remains explicit as the rule catalog evolves.

Connector-backed replay observations contain only the connector's reviewed normalized values. In
particular, the Elasticsearch R3 fixture stores the closed `logs.cause` classification and exact
Pod identity, never a raw log message, index or document identifier, query, label, URL, credential,
or user data.

Each fixture is versioned through its required `version` field. A fixture asserts the top verdict's
rule, confidence, scope, cited lens/predicate/value evidence, coverage gaps, fleet-wide flag, and
advisory shape. The harness evaluates every fixture twice and fails if the serialized result differs.
