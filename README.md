# Sith

**Status: Slice 1 local fleet source.** The CLI discovers every context resolved by client-go,
probes them independently, and reports reachable and unreachable clusters without a hub.

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

`sith clusters` follows standard client-go loading rules: set `KUBECONFIG` to an OS path-list or
use the default `~/.kube/config`. Exec-credential helpers run locally, exactly as they do for
`kubectl`; Sith does not copy kubeconfigs or credentials elsewhere.

Run the full local quality gate with golangci-lint v2.12.2 and govulncheck v1.6.0 on `PATH`:

```bash
make ci
```

The real multi-cluster gate creates two temporary kind clusters with a digest-pinned node image,
checks one additional unreachable context, and removes both clusters afterward. It requires a
running Docker engine and kind v0.32.0, and consumes additional CI time, disk, and memory:

```bash
make e2e-kind
```

The architecture, threat model, ADRs, and roadmap live under [`docs/`](docs/). Build-session
checkpoints are recorded under [`sessions/`](sessions/).
