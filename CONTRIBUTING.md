# Contributing to ccsession

Thanks for taking the time to contribute! Bug reports, feature ideas, and pull
requests are all welcome.

For anything larger than a small fix, please
[open an issue](https://github.com/sorafujitani/ccsession/issues) first so we
can agree on the approach before you invest time in a PR.

## Prerequisites

| Tool | Required for |
| --- | --- |
| [Go](https://go.dev/dl/) 1.25+ | building and testing |
| [`fzf`](https://github.com/junegunn/fzf) | running the interactive picker |
| `claude` ([Claude Code CLI](https://docs.claude.com/en/docs/claude-code)) | exercising `resume` end to end |
| [`opencode`](https://opencode.ai) | exercising the `--source=opencode` backend (optional) |

A [Nix](https://nixos.org/) flake is provided that pins Go, `fzf`, `gopls`, and
`goreleaser`. It is the quickest way to get a reproducible toolchain, but it is
optional — a plain Go install works fine.

## Getting started

```sh
# With Nix (recommended): drops you into a shell with the full toolchain.
nix develop

# Or with a local Go toolchain:
go build ./cmd/ccsession
```

Run the test suite before and after your change:

```sh
go test ./...
```

A few useful commands while developing:

```sh
go run ./cmd/ccsession            # list -> fzf -> resume
go run ./cmd/ccsession list       # emit TSV rows to stdout
go build ./cmd/ccsession && ./ccsession --version
```

## Project layout

```
cmd/ccsession/    entry point and CLI wiring
internal/
  ansi/           ANSI escape helpers
  config/         config-file + env + flag resolution
  grep/           full-text search over JSONL transcripts
  list/           TSV row rendering for fzf
  opencode/       OpenCode SQLite backend (sessions, preview, grep)
  preview/        preview-pane rendering
  resume/         chdir + exec of `claude --resume`
  session/        scanning and parsing of ~/.claude/projects/*.jsonl
  source/         backend abstraction (claude / opencode selection)
  timefmt/        relative-time formatting
```

Most contributions touch one `internal/` package plus its `_test.go`
neighbour. Keep packages focused and prefer adding to an existing one over
introducing a new top-level package.

## Code style

- Run `gofmt -l .` and make sure it reports nothing (CI enforces this).
- Run `go vet ./...` — it must pass clean.
- Match the surrounding code: naming, comment density, and idioms. Comments
  explain *why*, not *what*.
- Cover new behaviour with table-driven tests in the relevant `_test.go` file.

## Commit messages

Follow the existing [Conventional Commits](https://www.conventionalcommits.org/)
style used in the history. Use a type prefix and an imperative subject:

```
feat: make picker keybindings configurable
fix: skip oversized session lines
docs: order keybinding examples to match precedence
refactor: dedup shared helpers across packages
chore: bump goreleaser to 2.15.x
```

Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`.

## Pull requests

1. Branch off `main` and keep each PR focused on a single change.
2. Make sure `gofmt -l .` is clean and `go vet ./...` / `go test ./...` pass.
3. Update `README.md` (and any other docs) when you change user-facing
   behaviour, flags, or keybindings.
4. Fill in the pull request template and link the issue it addresses.
5. CI (`go vet`, `go build`, `go test`, `goreleaser check`) must be green
   before review.

## Pre-release smoke checks

`go test ./...` covers most behaviour, but the OpenCode backend talks to an
external SQLite store that the test suite drives through fixtures. Before
tagging a release, run a manual smoke test against a real OpenCode install:

1. Create at least one session with the latest OpenCode (run `opencode` and
   send a message or two so the session is persisted to its store).
2. Exercise each command path against that backend:
   - `ccsession --opencode` — the session shows up in the picker (**list**).
   - move the cursor onto it — the pane renders its messages (**preview**).
   - switch to grep mode and type a word from the session — it stays
     matched (**grep**).
   - press Enter — it resumes in the original directory (**resume**).

The Claude backend is exercised continuously in everyday use, so it needs no
separate checklist.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](./LICENSE) that covers this project.
