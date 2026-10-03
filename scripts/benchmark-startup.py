#!/usr/bin/env python3
"""Reproduce issue #124 with synthetic data only (Python stdlib + Go-built CLI)."""

import argparse
import fcntl
import hashlib
import json
import os
import pty
import select
import shlex
import shutil
import sqlite3
import statistics
import struct
import subprocess
import sys
import termios
import time
from pathlib import Path


def conversation(index, turn_bytes, updated_prompt=None):
    history = []
    for turn in range(41):
        prompt = f"synthetic kiro conversation {index} turn {turn}"
        if turn == 39 and updated_prompt is not None:
            prompt = updated_prompt
        content = {"Prompt": {"prompt": prompt}}
        if turn % 2 == 0:
            content = {"ToolUseResults": {"tool_use_results": []}}
        history.append(
            {
                "user": {"content": content, "timestamp": "2026-06-01T00:00:00Z"},
                "assistant": {"Response": {"content": "x" * turn_bytes}},
            }
        )
    return json.dumps({"history": history}, separators=(",", ":"))


def fixture(args):
    root = args.data_dir.resolve()
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    home = root / "home"
    cwd = home / "workspace"
    cwd.mkdir(parents=True, exist_ok=True)
    if sys.platform == "darwin":
        db_path = home / "Library/Application Support/kiro-cli/data.sqlite3"
    else:
        db_path = home / ".local/share/kiro-cli/data.sqlite3"
    config = {
        "version": 1,
        "rows": args.rows,
        "claude_rows": args.claude_rows,
        "turn_bytes": args.turn_bytes,
    }
    manifest = root / "fixture.json"
    if manifest.exists():
        try:
            saved = json.loads(manifest.read_text())
        except (OSError, json.JSONDecodeError) as error:
            raise ValueError("cannot read the synthetic fixture manifest") from error
        if not isinstance(saved, dict) or any(
            saved.get(key) != value for key, value in config.items()
        ):
            raise ValueError("fixture settings differ; use a new --data-dir")
        return root, db_path, saved
    if db_path.exists():
        raise ValueError("incomplete or unmanaged fixture; use a new --data-dir")
    db_path.parent.mkdir(parents=True, exist_ok=True)
    with sqlite3.connect(db_path) as db:
        db.execute("PRAGMA journal_mode=OFF")
        db.execute("PRAGMA synchronous=OFF")
        db.execute("""CREATE TABLE conversations_v2 (
            key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL,
            created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
            PRIMARY KEY (key, conversation_id))""")
        for index in range(args.rows):
            db.execute(
                "INSERT INTO conversations_v2 VALUES (?, ?, ?, ?, ?)",
                (
                    str(cwd),
                    f"classic-synthetic-{index:05}",
                    conversation(index, args.turn_bytes),
                    1000,
                    10000 + index,
                ),
            )
        config["json_bytes"] = db.execute(
            "SELECT sum(length(CAST(value AS BLOB))) FROM conversations_v2"
        ).fetchone()[0]
    projects = home / ".claude/projects/synthetic"
    projects.mkdir(parents=True, exist_ok=True)
    for index in range(args.claude_rows):
        session_id = f"11111111-1111-4111-8111-{index:012}"
        entry = {
            "type": "user",
            "timestamp": "2026-06-01T00:00:00Z",
            "cwd": str(cwd),
            "message": {
                "role": "user",
                "content": f"synthetic claude conversation {index}",
            },
        }
        (projects / f"{session_id}.jsonl").write_text(json.dumps(entry) + "\n")
    manifest.write_text(json.dumps(config, indent=2) + "\n")
    return root, db_path, config


def environment(root):
    env = dict(os.environ)
    for key in list(env):
        if key.startswith(
            ("CCSESSION_", "OPENCODE_", "PI_CODING_AGENT_", "PI_CONFIG_")
        ):
            del env[key]
    home = root / "home"
    env.update(
        {
            "HOME": str(home),
            "XDG_DATA_HOME": str(home / ".local/share"),
            "XDG_CONFIG_HOME": str(home / ".config"),
            "XDG_CACHE_HOME": str(home / ".cache"),
            "TERM": "xterm-256color",
            "CLAUDE_CONFIG_DIR": str(home / ".claude"),
            "KIRO_HOME": str(home / ".kiro"),
            "CODEX_HOME": str(home / ".codex"),
            "GROK_HOME": str(home / ".grok"),
            "CORTEX_HOME": str(home / ".cortex"),
            "PI_CODING_AGENT_SESSION_DIR": str(home / ".pi/agent/sessions"),
            "PI_CODING_AGENT_DIR": str(home / ".omp/agent"),
            "CCSESSION_SCAN_CACHE_DIR": str(root / "cache"),
        }
    )
    return env


def run_list(binary, env):
    start = time.perf_counter()
    result = subprocess.run(
        [str(binary), "--all", "list", "--json"],
        env=env,
        capture_output=True,
        check=True,
        timeout=180,
    )
    elapsed = time.perf_counter() - start
    try:
        rows = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("ccsession returned invalid list JSON") from error
    return elapsed, rows


def run_picker(binary, env, root):
    marker = root / "picker-loaded"
    marker.unlink(missing_ok=True)
    env = dict(
        env,
        FZF_DEFAULT_OPTS=(
            "--no-mouse --bind "
            + shlex.quote("load:execute-silent(touch " + shlex.quote(str(marker)) + ")")
        ),
    )
    env.pop("FZF_DEFAULT_OPTS_FILE", None)
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 160, 0, 0))

    def terminal():
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    start = time.perf_counter()
    proc = subprocess.Popen(
        [str(binary), "--all"],
        env=env,
        stdin=slave,
        stdout=slave,
        stderr=slave,
        preexec_fn=terminal,  # noqa: PLW1509 -- Single-threaded CLI needs a controlling PTY.
    )
    os.close(slave)
    loaded = None
    output = bytearray()
    try:
        while proc.poll() is None:
            if time.perf_counter() - start > 180:
                raise TimeoutError("picker did not finish")
            if select.select([master], [], [], 0.005)[0]:
                try:
                    output.extend(os.read(master, 65536))
                except OSError:
                    break
            # Wait for complete input AND a rendered session label, then escape
            # without selecting or resuming any session.
            if (
                loaded is None
                and marker.exists()
                and any(
                    label in output
                    for label in (
                        b"synthetic claude",
                        b"synthetic kiro",
                        b"updated prompt",
                    )
                )
            ):
                loaded = time.perf_counter() - start
                os.write(master, b"\x1b")
        proc.wait(timeout=10)
        if loaded is None or proc.returncode != 0:
            raise RuntimeError(
                "picker load failed: " + output[-2000:].decode(errors="replace")
            )
        return loaded
    finally:
        if proc.poll() is None:
            # Terminate this isolated PTY process group, including fzf/preview.
            import signal

            os.killpg(proc.pid, signal.SIGTERM)
            proc.wait(timeout=10)
        os.close(master)
        marker.unlink(missing_ok=True)


def update_one(db_path, turn_bytes, iteration=None):
    prompt = None if iteration is None else f"updated prompt {iteration}"
    raw = conversation(0, turn_bytes, prompt)
    with sqlite3.connect(db_path) as db:
        db.execute(
            "UPDATE conversations_v2 SET value=?, updated_at=? WHERE conversation_id=?",
            (
                raw,
                10000 if iteration is None else 20000 + iteration,
                "classic-synthetic-00000",
            ),
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--data-dir", type=Path, required=True)
    parser.add_argument("--rows", type=int, default=4451)
    parser.add_argument("--claude-rows", type=int, default=647)
    parser.add_argument("--turn-bytes", type=int, default=12400)
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--output", type=Path)
    parser.add_argument(
        "--picker", action="store_true", help="measure real fzf load in a PTY"
    )
    args = parser.parse_args()
    if min(args.rows, args.turn_bytes, args.repeats) < 1 or args.claude_rows < 0:
        parser.error(
            "rows, turn-bytes and repeats must be positive; claude-rows must be nonnegative"
        )
    root, db_path, config = fixture(args)
    report: dict[str, object] = {"fixture": config}
    if args.binary:
        binary = args.binary.resolve(strict=True)
        env = environment(root)
        update_one(db_path, args.turn_bytes)
        cases = {}
        expected = args.rows + args.claude_rows
        for case in ("cold", "warm", "updated"):
            if case != "cold":
                run_list(binary, env)
            timings = {"list_seconds": [], "picker_seconds": []}
            for iteration in range(args.repeats):
                for picker in [False, True] if args.picker else [False]:
                    try:
                        if case == "cold" and (root / "cache").exists():
                            shutil.rmtree(root / "cache")
                        if case == "updated":
                            update_one(
                                db_path, args.turn_bytes, 2 * iteration + int(picker)
                            )
                    except (OSError, sqlite3.Error) as error:
                        raise RuntimeError(
                            "cannot prepare the synthetic fixture"
                        ) from error
                    if picker:
                        timings["picker_seconds"].append(run_picker(binary, env, root))
                    else:
                        elapsed, rows = run_list(binary, env)
                        if len(rows) != expected:
                            raise AssertionError(
                                f"got {len(rows)} sessions, expected {expected}"
                            )
                        if case == "updated":
                            changed = next(
                                row
                                for row in rows
                                if row["id"] == "kiro:classic-synthetic-00000"
                            )
                            if changed["label"] != f"updated prompt {2 * iteration}":
                                raise AssertionError("updated label is stale")
                        timings["list_seconds"].append(elapsed)
            cases[case] = {
                key: {"samples": values, "median": statistics.median(values)}
                for key, values in timings.items()
                if values
            }
        report["cases"] = cases
        update_one(db_path, args.turn_bytes)
        _, rows = run_list(binary, env)
        stable = json.dumps(rows, sort_keys=True, separators=(",", ":")).encode()
        report["list_sha256"] = hashlib.sha256(stable).hexdigest()
        if args.output:
            args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
