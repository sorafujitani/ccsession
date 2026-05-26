# ccsession

`claude --resume` のための fzf フロントエンド。`~/.claude/projects` 配下の過去セッションを全プロジェクト横断で列挙し、プレビュー付きで選んで復帰する。

## 動作要件

| 種別 | コマンド | 必須 |
| --- | --- | --- |
| ランチャー | `fzf` | ◯ |
| 復帰先 | `claude` (Claude Code CLI) | ◯ |
| 中身 grep | `rg` (ripgrep) | `--grep` / grep モード時のみ |

## インストール

### a. `go install` (Go ツールチェイン持ち向け)

```sh
go install github.com/sorafujitani/ccsession/cmd/ccsession@latest
```

`go install` 経由でもバージョンは `runtime/debug.ReadBuildInfo` から復元されるので `ccsession --version` で確認できる。

### b. GitHub Release のバイナリ tarball

[Releases](https://github.com/sorafujitani/ccsession/releases) から OS/Arch に合った `ccsession_<ver>_<os>_<arch>.tar.gz` を落として展開し、PATH の通った場所へ置く。

```sh
tar xzf ccsession_0.1.0_darwin_arm64.tar.gz
install -m 0755 ccsession ~/.local/bin/
```

macOS の Gatekeeper に弾かれた場合は一度だけ:

```sh
xattr -d com.apple.quarantine ~/.local/bin/ccsession
```

### c. Nix flake

一度だけ実行:

```sh
nix run github:sorafujitani/ccsession
```

プロファイルに入れる:

```sh
nix profile install github:sorafujitani/ccsession
```

### d. Homebrew (将来)

`.goreleaser.yaml` に tap 用の下書きを残してあるので、`sorafujitani/homebrew-tap` を用意し、対応する `brews:` セクションを有効化すれば `brew install sorafujitani/tap/ccsession` で入るようになる。

## 使い方

```sh
ccsession                      # list -> fzf -> resume の一括フロー
ccsession list  [--grep Q]     # TSV 行を stdout に出す
ccsession preview <id>         # プレビュー描画
ccsession resume  <id>         # 元 cwd に cd して `claude --resume <id>` を exec
ccsession --version
ccsession --help
```

fzf 内のキー:

- `Ctrl-G`: grep モード (キーストロークごとに ripgrep で再フィルタ)
- `Ctrl-O`: dir モード (ディレクトリ名カラムだけに fuzzy マッチ)
- `Ctrl-F`: fuzzy モードへ戻る (時刻/ディレクトリ名/ラベルを横断)
- `Enter`: 選択して resume
- `ESC`: キャンセル

## 開発

```sh
nix develop                    # Go + fzf + rg + gopls + goreleaser が揃った shell
go build ./cmd/ccsession
go test ./...
```

### ローカルでリリース成果物を確認

```sh
goreleaser release --snapshot --clean --skip=publish
ls dist/
```

### Nix で直接ビルド

```sh
nix build
./result/bin/ccsession --version
```
