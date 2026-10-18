# Contributing

Thanks for helping. Bug reports, ideas and pull requests are welcome; for a bigger change, open an issue first so we can agree on the approach.

## Setup

You need Go (the version in `go.mod`) and git. [just](https://github.com/casey/just) is optional: each recipe is one or two plain commands, listed in the `justfile`.

```sh
git clone https://github.com/Maduki-tech/agentpr
cd agentpr
just build          # go build -o agentpr .
just run            # go run . (run it inside any git repo)
```

## Before you open a PR

```sh
just check          # gofmt check, go vet ./..., go test -race ./...
```

CI runs the same steps and prints the test coverage (`just cover` locally). `just lint` adds [staticcheck](https://staticcheck.dev) when it's installed.

## Code style

- Match the code around you: small functions, few files, comments that say why.
- Keep it simple. Prefer the standard library and the dependencies already in `go.mod`; add a dependency only when a few lines can't do the job.
- A `ponytail:` comment marks a deliberate shortcut and names its limit and the way out, e.g. `// ponytail: polls git every 1.5s; switch to inotify if big repos make this slow.` Add one when you take a shortcut like that.
- Add a test for logic with branches; keep it small.

## Words and decisions

[GLOSSARY.md](GLOSSARY.md) defines the domain terms (Changeset, Base, Thread, Review…). Use them in code, comments and docs, and update the glossary when a term changes.

[docs/adr](docs/adr) records the decisions that shaped agentpr. Add a short ADR when you make a decision someone would otherwise question later.

## Screenshots

The images in `docs/images/` are generated. After a change to the UI, regenerate them:

```sh
just screenshots    # docs/screenshots.sh
```

It needs tmux and ImageMagick. It builds agentpr, runs it in a detached tmux session on a demo repo with fake `claude` and `gh` commands, and renders each screen with [freeze](https://github.com/charmbracelet/freeze).

## Pull requests

- One topic per PR, with a description of what changed and why.
- Update the README and `docs/` when behavior changes.
- `just check` must pass.
