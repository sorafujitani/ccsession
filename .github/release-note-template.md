# Release Note フォーマット

ccsession のリリースノートはこの構成で記述する。`gh release edit <tag> --notes-file <file>` で反映する
（goreleaser が自動生成する changelog を、この手書きノートで上書きする運用）。

## 全体構成

1. **見出し** — `## ccsession vX.Y.Z` とワンライナーの製品説明。
2. **セクション** — 変更を種別ごとに分ける。該当が無いセクションは省略してよい。
   - `### Features` — 新機能
   - `### Fixes` — バグ修正
   - `### Internal improvements` — リファクタ・性能・内部改善（利用者の挙動を変えないもの）
   - `### Contributors` — 謝辞
3. **フッター** — 区切り線とインストール導線。

## 記法ルール

- 言語は **英語**。
- 各項目は `- **<要約>** by @<handle> (#<PR>): <説明>` の形式。
  - コントリビューターは `@handle` で**必ずメンション**する（Release ページでリンク＆通知される）。
  - 関連 PR があれば `(#番号)` を添える。
  - セクション全体が同一著者なら `### Internal improvements (by @handle)` のように見出しへまとめてもよい。
- 種別は Conventional Commits の prefix に対応させる（`feat:`→Features / `fix:`→Fixes / `refactor:`・`perf:`→Internal improvements）。
- `docs:` / `test:` / `chore:` / `ci:` は原則ノートに載せない（`.goreleaser.yaml` の changelog filter と揃える）。

## テンプレート

```markdown
## ccsession vX.Y.Z

An fzf frontend for `claude --resume`.

### Features
- **<feature summary>** by @<handle> (#<PR>): <what changed and why it matters>.

### Fixes
- <fix summary> — by @<handle> (#<PR>).

### Internal improvements (by @<handle>)
- <refactor / perf / internal change>.

### Contributors
Thanks to @<handle1> and @<handle2> for the work in this release!

---
See the README for installation instructions.
```

## 実例 (v0.1.4)

```markdown
## ccsession v0.1.4

An fzf frontend for `claude --resume`.

### Features
- **Highlight grep query matches** by @sorafujitani (#17): matched substrings of the grep query are now shown in reverse video in both the list and preview panes.
- **`CLAUDE_CONFIG_DIR` support** by @mozumasu (#10): sessions are now discovered under `$CLAUDE_CONFIG_DIR/projects`, so customized Claude Code config directories work out of the box.
- **Homebrew tap distribution** by @sorafujitani: install via `brew install sorafujitani/tap/ccsession`.

### Fixes
- `ProjectsDir` now correctly resolves `$CLAUDE_CONFIG_DIR/projects` — by @mozumasu (#10).

### Internal improvements (by @sorafujitani)
- Deduplicated helpers shared across packages (`internal/ansi`, `timefmt.Parse`, `session.ExtractText` / `Truncate`).
- Simplified code using Go 1.22 loop-variable semantics and the builtin `max` / `min` / range-over-int.
- Bounded preview message loading with a ring buffer to cut memory use on large sessions.

### Contributors
Thanks to @sorafujitani and @mozumasu for the work in this release!

---
See the README for installation instructions.
```

## リリース手順の対応

1. `git tag -a vX.Y.Z -m "ccsession vX.Y.Z" && git push origin vX.Y.Z`
2. `release.yml` (goreleaser) が走り、バイナリ・Homebrew tap・Release を生成。
3. 上記テンプレートでノートを作成し `gh release edit vX.Y.Z --notes-file <file>` で反映。
