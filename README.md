# Sith

**Status: Slice 0 foundation.** The local-first CLI walking skeleton is runnable; Kubernetes
context discovery arrives in Slice 1.

Sith is ArdurAI's single-binary, local-first Kubernetes fleet tool: **k9s for your whole fleet**.
It is designed to aggregate every kubeconfig context without an account, telemetry, or cluster
data leaving the machine. The same source-abstract fleet model will later power an optional
governed hub.

## Build and run

Sith requires a supported Go 1.26 toolchain.

```bash
make build
./bin/sith version
./bin/sith version --output json
./bin/sith clusters
```

Slice 0 intentionally returns a typed empty fleet through the stubbed `fleet.Source` seam. Run the
full local quality gate with a pinned golangci-lint v2.12.2 on `PATH`:

```bash
make ci
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
