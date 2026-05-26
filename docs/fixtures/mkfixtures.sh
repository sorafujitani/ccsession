#!/bin/bash
set -u
BASE="/tmp/fakehome/.claude/projects"
rm -rf "$BASE"
mkdir -p "$BASE"

# 1) Normal session
mkdir -p "$BASE/-tmp-proj-normal"
cat > "$BASE/-tmp-proj-normal/11111111-1111-1111-1111-111111111111.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-normal","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"hello world"}}
{"type":"assistant","timestamp":"2026-05-25T10:00:01.000Z","message":{"role":"assistant","content":"hi"}}
{"type":"ai-title","aiTitle":"normal title"}
{"type":"user","cwd":"/tmp/proj-normal","timestamp":"2026-05-25T10:01:00.000Z","message":{"role":"user","content":"second message"}}
{"type":"last-prompt","lastPrompt":"latest prompt"}
JSON

mkdir -p /tmp/proj-normal

# 2) Session with no user messages (should be excluded)
mkdir -p "$BASE/-tmp-proj-nouser"
cat > "$BASE/-tmp-proj-nouser/22222222-2222-2222-2222-222222222222.jsonl" <<'JSON'
{"type":"assistant","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"assistant","content":"hello"}}
{"type":"ai-title","aiTitle":"no user"}
JSON

# 3) Malformed JSONL
mkdir -p "$BASE/-tmp-proj-broken"
cat > "$BASE/-tmp-proj-broken/33333333-3333-3333-3333-333333333333.jsonl" <<'JSON'
this is not json at all
{"type":"user","cwd":"/tmp/proj-broken","timestamp":"2026-05-25T11:00:00.000Z","message":{"role":"user","content":"survived"}}
{broken json
JSON
mkdir -p /tmp/proj-broken

# 4) Session with very long label (>200 chars)
mkdir -p "$BASE/-tmp-proj-long"
LONG=$(python3 -c "print('a' * 500)")
cat > "$BASE/-tmp-proj-long/44444444-4444-4444-4444-444444444444.jsonl" <<JSON
{"type":"user","cwd":"/tmp/proj-long","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"$LONG"}}
JSON
mkdir -p /tmp/proj-long

# 5) Session with control chars / multi-line content
mkdir -p "$BASE/-tmp-proj-multiline"
python3 -c '
import json
m = "line1\nline2\tindented\r carriage"
e = {"type":"user","cwd":"/tmp/proj-multiline","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":m}}
print(json.dumps(e))
' > "$BASE/-tmp-proj-multiline/55555555-5555-5555-5555-555555555555.jsonl"
mkdir -p /tmp/proj-multiline

# 6) Session with no timestamps
mkdir -p "$BASE/-tmp-proj-nots"
cat > "$BASE/-tmp-proj-nots/66666666-6666-6666-6666-666666666666.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-nots","message":{"role":"user","content":"no timestamp"}}
JSON
mkdir -p /tmp/proj-nots

# 7) Session whose cwd doesn't exist
mkdir -p "$BASE/-tmp-proj-gone"
cat > "$BASE/-tmp-proj-gone/77777777-7777-7777-7777-777777777777.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-does-not-exist","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"missing dir"}}
JSON

# 8) Project dir name with hyphens in path components (lossy decoding test)
# Original cwd: /home/foo-bar/proj  -> encoded: -home-foo-bar-proj
mkdir -p "$BASE/-home-foo-bar-proj"
cat > "$BASE/-home-foo-bar-proj/88888888-8888-8888-8888-888888888888.jsonl" <<'JSON'
{"type":"user","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"hyphen test"}}
JSON

# 9) Content as blocks array
mkdir -p "$BASE/-tmp-proj-blocks"
cat > "$BASE/-tmp-proj-blocks/99999999-9999-9999-9999-999999999999.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-blocks","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}}
JSON
mkdir -p /tmp/proj-blocks

# 10) agent-*.jsonl should be excluded
mkdir -p "$BASE/-tmp-proj-agent"
cat > "$BASE/-tmp-proj-agent/agent-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-agent","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"agent file"}}
JSON

# 11) Empty file
mkdir -p "$BASE/-tmp-proj-empty"
touch "$BASE/-tmp-proj-empty/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.jsonl"

# 12) Future timestamp
mkdir -p "$BASE/-tmp-proj-future"
cat > "$BASE/-tmp-proj-future/cccccccc-cccc-cccc-cccc-cccccccccccc.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-future","timestamp":"2099-01-01T00:00:00.000Z","message":{"role":"user","content":"from the future"}}
JSON
mkdir -p /tmp/proj-future

# 13) Whitespace-only label
mkdir -p "$BASE/-tmp-proj-blank"
cat > "$BASE/-tmp-proj-blank/dddddddd-dddd-dddd-dddd-dddddddddddd.jsonl" <<'JSON'
{"type":"user","cwd":"/tmp/proj-blank","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":"   "}}
JSON
mkdir -p /tmp/proj-blank

# 14) ANSI codes in label (potential injection)
mkdir -p "$BASE/-tmp-proj-ansi"
python3 -c '
import json
m = "\x1b[31mRED\x1b[0m hidden\x07 bell"
e = {"type":"user","cwd":"/tmp/proj-ansi","timestamp":"2026-05-25T10:00:00.000Z","message":{"role":"user","content":m}}
print(json.dumps(e))
' > "$BASE/-tmp-proj-ansi/eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee.jsonl"
mkdir -p /tmp/proj-ansi

echo "fixtures created"
ls "$BASE"
