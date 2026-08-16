# Contributing

Thanks for taking a look at onebox.

## Setup

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./...
```

For frontend changes, see [web/](web) — `npm install && npm run build`
from there regenerates the embedded dashboard at `internal/webui/app/dist`,
which `go:embed` bakes into the binary. Both `go build` and `npm run build`
must be run (and their output committed) for a frontend change to actually
ship.

See [README.md](README.md#development) for the full local dev + browser
verification workflow, and [ARCHITECTURE.md](ARCHITECTURE.md) for how the
pieces fit together before touching the AI chat/tool-execution system in
particular — it has a couple of documented, regression-prone bugs worth
reading about first.

## Before opening a PR

- `go build ./... && go vet ./... && gofmt -l . && go test ./...` must be clean.
- If you touched `web/`, `npm run build` must be clean and the resulting
  `internal/webui/app/dist` changes committed alongside your source change.
- Keep PRs focused — one change, one PR. See [ROADMAP.md](ROADMAP.md) for
  what's explicitly out of scope before proposing something large.

## Reporting bugs / requesting features

Open a GitHub issue. Include your OS, how you ran onebox (built from
source vs. a release binary), and steps to reproduce for bugs.
