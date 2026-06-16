---
name: ccsession
description: Recover, inspect, and hand off to local agent sessions with the ccsession CLI. Use when the user wants to find prior context, locate a historical Claude Code/OpenCode/Grok/Codex session, compare candidate sessions, preview a past conversation, or resume work that happened in another agent session.
---

# ccsession

## Overview

Use `ccsession` as a discovery and handoff tool. Inspecting a session is safe and read-only; resuming a session starts or replaces an interactive agent process and requires explicit user confirmation.

Do not duplicate ccsession's search, preview, or resume logic. Drive the CLI, summarize what it returns, and let the user choose before launching anything interactive.

## Workflow

1. Gather the search intent: topic, issue number, repository, working directory, source backend, approximate date, or any phrase likely to appear in the transcript.
2. Search before previewing. Prefer content search when the user gives a topic or issue:

   ```sh
   ccsession list --grep "<query>" --color=never
   ```

   If the intent is a repository, working directory, or time period rather than transcript text, start from the plain list and narrow the candidate set from the metadata:

   ```sh
   ccsession list --color=never
   ```

   Put global source flags before the subcommand:

   ```sh
   ccsession --codex list --grep "<query>" --color=never
   ccsession --source all list --grep "<query>" --color=never
   ```

   Repeat the same source selector on preview and resume commands. Global source flags affect only that `ccsession` process and the fzf children it starts.
3. Present a small candidate set instead of dumping raw output. Include source when known, session id, locator if available, cwd or cwd basename, label/title, last activity, why it matched, and a match snippet when the CLI provides one. Current TSV output has no snippet column; use preview to inspect the match text.
4. Preview the selected candidate before recommending resume:

   ```sh
   ccsession preview --query "<query>" "<session-id>"
   ccsession --source all preview --query "<query>" "<session-id>"
   ```

   If `ccsession list` produced a locator column, preserve it and use:

   ```sh
   ccsession preview --locator "<locator>" --query "<query>" "<session-id>"
   ccsession --source all preview --locator "<locator>" --query "<query>" "<session-id>"
   ```

5. After previewing, explain whether the session appears relevant. Keep the distinction clear: previewing only reads past context; resuming launches the underlying agent CLI in the recorded working directory.
6. Ask for explicit confirmation before running `ccsession resume` or the interactive picker (`ccsession` with no subcommand). Do not infer confirmation from the user's interest in a candidate.
7. Run resume only after confirmation:

   ```sh
   ccsession resume "<session-id>"
   ccsession --source all resume "<session-id>"
   ```

   With a locator:

   ```sh
   ccsession resume --locator "<locator>" "<session-id>"
   ccsession --source all resume --locator "<locator>" "<session-id>"
   ```

## Current CLI Shape

Current `ccsession list` output is TSV intended for fzf. Treat it as the current integration path only, and disable color when parsing:

```text
session id<TAB>locator<TAB>last epoch<TAB>relative time<TAB>cwd basename<TAB>label
```

Use `--regex` only when the user asks for regex semantics or a fixed-string query is insufficient. Use `--exclude-dir <text>` when noisy directories obscure the result.

## Preferred Future Shape

Once available, prefer structured and non-launching commands over TSV parsing and direct resume launch:

```sh
ccsession list --json --grep "<query>" --limit 5
ccsession resume-spec "<session-id>"
```

Use a non-launching resume-spec command to show the exact backend, cwd, binary, and arguments before asking the user whether to launch the interactive resume.
