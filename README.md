# ccsession

> An fzf-powered session picker for resuming local agent sessions.

![ccsession demo](./docs/assets/ccsession_demo.gif)

`ccsession` lists local agent sessions (Claude Code by default, with optional
OpenCode, Grok, and Codex backends), lets you fuzzy-find across all of your
projects with a live preview pane, and resumes the one you pick in its original
working directory.

## Features

- **Cross-project listing** — every session from every project in one view,
  sorted by last activity.
- **Three search modes** — fuzzy (default), directory-only, and full-text
  grep over JSONL transcripts, with configurable mode-switch keys.
- **Live preview** — last 30 messages of the highlighted session, with
  timestamps and roles. In grep mode the matched query is highlighted in the
  preview so you can spot the hit at a glance.
- **Faithful resume** — `chdir`s back to the session's original `cwd` before
  exec'ing the selected agent's resume command, so paths and tooling Just Work.
- **Single static binary** — written in Go with no cgo; bundles a pure-Go
  SQLite reader (for OpenCode support) and a small TOML parser for the
  optional config file.

## Requirements

| Tool | Required for |
| --- | --- |
| [`fzf`](https://github.com/junegunn/fzf) `>= 0.58.0` | interactive picker |
| `claude` ([Claude Code CLI](https://docs.claude.com/en/docs/claude-code)) | resuming sessions |
| [`opencode`](https://opencode.ai) | listing & resuming OpenCode sessions (only with `--source=opencode`) |
| `grok` (Grok Build TUI) | listing & resuming Grok sessions (only with `--source=grok`) |
| `codex` (Codex CLI) | listing & resuming Codex sessions (only with `--source=codex`) |

`ccsession` depends on newer `fzf` actions such as `transform`, `rebind`,
`unbind`, `disable-search`, and `change-nth`. The newest of those,
`change-nth`, landed in `fzf 0.58.0`, so older versions may start but the mode
switch bindings will not work correctly.

## Install

### Go

```sh
go install github.com/sorafujitani/ccsession/cmd/ccsession@latest
```

Requires Go 1.25 or newer (the pure-Go SQLite reader for OpenCode support
needs it; see #52).

Version metadata is recovered from `runtime/debug.ReadBuildInfo`, so
`ccsession --version` works for `go install` builds as well.

### Pre-built binaries

Grab the `ccsession_<ver>_<os>_<arch>.tar.gz` for your platform from the
[Releases](https://github.com/sorafujitani/ccsession/releases) page, extract
it, and drop the binary somewhere on your `PATH`:

```sh
tar xzf ccsession_0.1.0_darwin_arm64.tar.gz
install -m 0755 ccsession ~/.local/bin/
```

If macOS Gatekeeper complains:

```sh
xattr -d com.apple.quarantine ~/.local/bin/ccsession
```

### Nix flake

```sh
nix run github:sorafujitani/ccsession             # one-off
nix profile install github:sorafujitani/ccsession # install into a profile
```

### Homebrew

```sh
brew install sorafujitani/tap/ccsession
```

The formula lives in
[`sorafujitani/homebrew-tap`](https://github.com/sorafujitani/homebrew-tap)
and GoReleaser refreshes it on every tagged release. `fzf` is installed as a
dependency; the `claude` CLI must be installed separately. `opencode`, `grok`,
and `codex` are needed only with their matching `--source` backends — they back
optional features (unlike `fzf`, which is always required), so they are
intentionally left out of the formula's `depends_on`.

## Usage

```sh
ccsession                            # list -> fzf -> resume
ccsession --grok                     # use Grok sessions from ~/.grok/sessions
ccsession --codex                    # use Codex sessions from ~/.codex/sessions
ccsession list  [--grep Q] [--regex] # emit TSV rows to stdout
ccsession list --json --grep Q --limit 5 # emit structured rows for agents
ccsession preview [--query Q] [--regex] <id> # render the preview pane (Q highlighted)
ccsession preview --json <id>        # emit structured preview data for scripts/agents
ccsession resume-spec <id>           # print the resume target without launching it
ccsession resume  <id>               # chdir to the session's cwd, exec the selected agent
ccsession --version
ccsession --help
```

## Agent Skill

This repository ships a Codex Agent Skill at
`.agents/skills/ccsession`. Use `$ccsession` when you want an agent to recover
prior context, compare historical sessions, preview a likely match, or hand off
to a previous local agent session.

When Codex is working from this repository checkout, Codex discovers the
repo-local skill from `.agents/skills/ccsession`. Invoke it explicitly with
`$ccsession` or ask for the same workflow in natural language, for example:

```text
Use $ccsession to find the session where we worked on issue 84.
```

If you installed only the `ccsession` binary and want the skill available from
other repositories, install it with the `skills` CLI:

```sh
npx skills add sorafujitani/ccsession --skill ccsession
```

Here `--skill ccsession` selects this skill from the repository. The skill
itself is the standard Agent Skills directory format: a folder with `SKILL.md`
plus optional resources.

For a user-wide install:

```sh
npx skills add sorafujitani/ccsession --skill ccsession -g
```

When developing from a local checkout, install from the current directory:

```sh
npx skills add . --skill ccsession
```

Start a new Codex session after installing or updating the skill so the skill
metadata is reloaded. The skill assumes the `ccsession` binary is on `PATH`.

The skill teaches agents to use `ccsession` in a read-first workflow:

1. Search candidates with structured output:

   ```sh
   ccsession list --json --grep "<query>" --limit 5
   ```

   Use a source selector when the target backend is known:

   ```sh
   ccsession --codex list --json --grep "<query>" --limit 5
   ccsession --source all list --json --grep "<query>" --limit 5
   ```

2. Summarize a small candidate set for the user. The JSON rows include
   `source`, `id`, `locator`, `cwd`, `cwd_basename`, `label`,
   `last_activity`, `cwd_exists`, and `cwd_unknown`.
3. Preview the selected candidate before recommending resume:

   ```sh
   ccsession preview --locator "<locator>" --query "<query>" "<id>"
   ccsession preview --json --locator "<locator>" "<id>"
   ```

4. Show the non-launching handoff target:

   ```sh
   ccsession resume-spec --locator "<locator>" "<id>"
   ```

   `resume-spec` prints the selected backend, working directory, binary, and
   arguments as JSON. It does not start an interactive process.
5. Run `ccsession resume --locator "<locator>" "<id>"` only after explicit user
   confirmation. `resume` changes into the recorded `cwd` and `exec`s the
   selected agent CLI, replacing the current process.

When using `--source`, repeat the same source selector on `list`, `preview`,
`resume-spec`, and `resume`; global flags apply only to that `ccsession`
process and the fzf children it starts.

### Keys inside fzf

| Key      | Mode |
| -------- | --- |
| `Ctrl-G` | grep — refilters by user/assistant content on every keystroke; matches are highlighted in the preview |
| `Ctrl-O` | dir — fuzzy match restricted to the directory column |
| `Ctrl-F` | fuzzy — default; matches across time / dir / label |
| `Enter`  | resume the selected session |
| `Esc`    | cancel |

The three mode-switch keys are the defaults and can be overridden (see below).

### Configuring the keybindings

If a mode-switch key clashes with your terminal, shell, or muscle memory, you
can remap any of the three. Keys are resolved in this order (first wins):

**CLI flags > environment variables > config file > defaults**

The on-screen header is regenerated from the resolved keys, so the hint always
matches what is active.

```sh
# CLI flags (highest precedence)
ccsession --bind-grep ctrl-r --bind-fuzzy alt-f

# environment variables
export CCSESSION_BIND_GREP=ctrl-r
export CCSESSION_BIND_DIR=ctrl-o
export CCSESSION_BIND_FUZZY=alt-f
```

Config file at `~/.config/ccsession/config.toml` (lowest precedence before
defaults; honors `XDG_CONFIG_HOME`). ccsession only **reads** this file — it
never creates it, so create it yourself only if you want file-based overrides:

```toml
[keybindings]
grep  = "ctrl-r"
dir   = "ctrl-o"
fuzzy = "alt-f"
```

Any key you leave unset falls through to the next source. A key name must be
lower-case fzf syntax (`ctrl-r`, `alt-f`, `f1`, …); the three keys must be
distinct and must not be a reserved fzf event name (`enter`, `change`, …), or
ccsession exits with an error instead of starting the picker.

## How it works

1. `ccsession list` reads the selected backend (`~/.claude/projects/*/` by
   default, or `--source=opencode` / `--source=grok` / `--source=codex`) and prints one TSV row
   per session (`id`, `locator`, `epoch`, relative time, cwd basename, label).
   `ccsession list --json --limit N` emits the same candidates as a JSON array
   for agent integrations.
2. `fzf` consumes the TSV. The three key bindings swap fzf's matcher
   between fuzzy mode, directory-only mode, and grep mode (which reloads
   via `ccsession list --grep <query>` on every keystroke). The current
   query is also forwarded to the preview as `ccsession preview --query
   <query> <id>`, which highlights its matches in the rendered messages.
3. `ccsession resume-spec <id>` resolves the same target as `resume` and prints
   the source, cwd, binary, and arguments as JSON without launching anything.
4. On `Enter`, `ccsession resume <id>` resolves the session's original
   `cwd`, `chdir`s into it, and `execve`s the selected agent's resume command
   so the resumed process fully replaces the picker.

Backend-specific homes can be overridden with `GROK_HOME` for Grok and
`CODEX_HOME` for Codex. Codex defaults to `~/.codex`, reading sessions from
its `sessions` subdirectory.

## Development

```sh
nix develop                    # Go + fzf + gopls + goreleaser
go build ./cmd/ccsession
go test ./...
```

### Snapshot a release locally

```sh
goreleaser release --snapshot --clean --skip=publish
ls dist/
```

### Build with Nix

```sh
nix build
./result/bin/ccsession --version
```

## Contributing

Bug reports and pull requests are welcome at
<https://github.com/sorafujitani/ccsession>. For larger changes, please
open an issue first to discuss what you'd like to change.

## License

[MIT](./LICENSE)
