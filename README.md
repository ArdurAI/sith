# Sith

**Status: Slice 2 cache-first fleet client.** The CLI discovers every context resolved by
client-go, hydrates a local in-memory fleet cache, and serves Tier-1 reads and cross-cluster search
from normalized snapshots with explicit freshness and coverage.

Sith is ArdurAI's single-binary, local-first Kubernetes fleet tool: **k9s for your whole fleet**.
It is designed to aggregate every kubeconfig context without an account, telemetry, or cluster
data leaving the machine. The same source-abstract fleet model will later power an optional
governed hub.

## Build and run

Sith requires a supported Go 1.26 toolchain.

```bash
make build
./bin/sith                       # interactive terminal: cache-first fleet view
./bin/sith tui                   # explicit equivalent
./bin/sith version
./bin/sith version --output json
./bin/sith clusters
./bin/sith get pods -A --all-clusters
./bin/sith search 'image:*log4j*'
./bin/sith correlate 'deploy/payments status!=Healthy'
```

`sith clusters` follows standard client-go loading rules: set `KUBECONFIG` to an OS path-list or
use the default `~/.kube/config`. Exec-credential helpers run locally, exactly as they do for
`kubectl`; Sith does not copy kubeconfigs or credentials elsewhere.

Scripted `get` calls require either `--all-clusters` or one explicit `--context`. Text, JSON,
wide, and source-abstract name outputs are supported. Search and correlation run over the same
normalized in-memory records; partial results name stale/unreachable contexts. The cache is not
persisted to disk, so raw workload specifications do not become a new plaintext credential-adjacent
artifact.

The TUI opens only when stdin and stdout are terminals; redirected bare invocations remain
script-safe and print help. Tier-1 lenses are Pods, Deployments, Events, and Nodes. Use `:` for
lens/context commands, `/` to filter the current lens, `Ctrl-K` for whole-fleet fuzzy/structured
search, number keys for cluster scope, `c` for coverage, and `Ctrl-R` for a non-blocking refresh.
The UI uses Bubble Tea v2.0.8 core only; tables and search remain local so no optional styling or
component dependency enters the binary.

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
