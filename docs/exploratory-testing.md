# Exploratory Testing Report

`ccsession` 探索的テストの記録。実バイナリ (`go build ./cmd/ccsession`,
commit `5500db8`) を `HOME` 上書きで合成セッションディレクトリに向けて
走らせ、`list` / `preview` / `resume` / `--grep` の各経路を一通り叩いた
結果見つかった不具合と気になり点をまとめる。

## テスト環境

- Go 1.24.7 / Linux amd64 / fzf 0.44.1 / ripgrep 14.x
- 偽 `HOME`: `/tmp/fakehome`、配下に 14 種類のフィクスチャを配置
  (正常 / no-user / 壊れた JSONL / 超長ラベル / 制御文字入り / 無タイムスタンプ
  / cwd 喪失 / ハイフン含み cwd / blocks 配列コンテンツ / `agent-*.jsonl` /
  空ファイル / 未来日時 / 空白ラベル / ANSI 入りラベル)
- 検証コマンド: `HOME=/tmp/fakehome ./ccsession list [--grep …] [--no-color]`
  `./ccsession preview <id>` / `./ccsession resume <id>`

## 不具合一覧

重大度の目安: **High** = 動作が誤っているか出力を信用できない /
**Medium** = UX が壊れる / **Low** = 表示・整形の細かい所。

### High

#### B-1: 空白だけのラベルが除外されない

`internal/session/parse.go:84` で `firstNonEmpty(aiTitle, lastPrompt, lastUserText)`
が空文字列だけを除外している。`"   "` のような空白だけの文字列は
non-empty 扱いで通過し、その後 `sanitizeLabel` が trim して空文字列に
なる。結果として **ラベル列が空のセッション** が `list` に並ぶ。

```
$ ccsession list --no-color | awk -F'\t' '$5=="" {print $1}'
dddddddd-dddd-dddd-dddd-dddddddddddd
```

修正案: `sanitizeLabel` 後の長さで再度足切りする、もしくは
`firstNonEmpty` を `strings.TrimSpace` 付きの実装に差し替える。

#### B-2: 未来日時のセッションが一覧の先頭に float する

`internal/session/scan.go:120` のソートが `LastEpoch` 降順なので、
タイムスタンプが未来 (例: テストの 2099 年) のセッションが現行のセッション
より上に並ぶ。`Relative` は `t.After(now)` を `now` に丸めるので表示は
`just now` になり、**「just now なのに最古ですらない並びに居る」** という
見た目になる。`claude` 側が時刻不整合の transcript を書いた場合に
ノイズになる。

```
$ ccsession list --no-color | head -2
cccccccc-...    4070908800    just now    proj-future    from the future
66666666-...    1779773397    2m ago      proj-nots      no timestamp
```

修正案: ソート用エポックを `min(t, now)` でクランプする、または未来日時を
明示的に弾く。

#### B-3: ファイル実在でも "session not found" を返す

`FindByID` は `ParseSessionTail` が `(nil, nil)` (= user メッセージなし /
ラベル決定不能 / 空ファイル) を返した場合、呼び出し側は file ありなしを
区別せず `s == nil` で "session not found" にしている
(`resume.go:19`, `preview.go:53`)。

```
$ ls /tmp/fakehome/.claude/projects/-tmp-proj-nouser/
22222222-2222-2222-2222-222222222222.jsonl   # ← 実在
$ ccsession preview 22222222-2222-2222-2222-222222222222
ccsession preview: session not found: 22222222-2222-2222-2222-222222222222
```

ユーザがファイルパスをコピペで指定したケースで原因究明が難しい。
"session has no usable content" のような区別したメッセージにすべき。

#### B-7: `--grep` が JSONL の構造に漏れて誤マッチ大量発生

`internal/grep/grep.go:27` は `rg -i --glob '*.jsonl'` で JSONL ファイルの
**生バイト**を検索する。結果として、

- `--grep type` → JSON キー `"type"` がほぼ全行にあるので **全セッションが**
  ヒット (テストでは 13/13)。
- `--grep proj-normal` → cwd フィールドの値にもマッチ。意図せず
  ディレクトリ名検索になる。
- ペイロードが JSON エスケープ (`あ`, `\n`, `\"`) で記録されていると
  ユーザが自然に打った文字列と一致せず取りこぼす。

修正案: 検索対象を user/assistant の `content` テキストに限定する
パースベースの実装に置き換える、もしくは README で「JSONL バイト検索」
であることを明示する。

#### B-8: ripgrep の正規表現エラーがそのまま漏れる

grep モードはキー入力ごとに `rg --grep '<q>'` を再実行するため、未完成の
正規表現 (例: 入力中の `[`) で rg が exit 2 になる。コマンドラインなら
stderr に `regex parse error` が出るが、fzf reload 経由だと **画面上は
空リスト** になるだけで原因が見えない。

```
$ ccsession list --grep '[invalid'
ccsession list: ripgrep failed (exit 2): rg: regex parse error:
    (?:[invalid)
       ^
error: unclosed character class
```

修正案: デフォルトで `rg -F` (fixed-strings) を使うか、`exit 2` を
"no match" に丸めて UI 側で `(invalid query)` ヘッダを出す。

### Medium

#### B-4: プレビューの per-message タイムスタンプが `00:00` になる

`internal/preview/preview.go:112` は `m.Timestamp.Local().Format("15:04")`
を表示するが、タイムスタンプ欠落エントリは `time.Time{}` のまま渡るので
**真夜中の UTC が `00:00` として表示**される。実際の時刻が 00:00 だったの
か欠落だったのか区別できない。

```
$ ccsession preview 66666666-...
[user 00:00] no timestamp   ← 実際は時刻不明
```

修正案: ゼロ値は `--:--` に置換するか、メッセージごとに `IsZero()` を
チェックして時刻フィールドを省略する。

#### B-5: プレビューヘッダの "started" 行が矛盾表示する

未来日時セッションの `started` 行が `2099-01-01 00:00  (just now)` と
出る。絶対値 (2099) と相対表現 (just now) が食い違って一見バグに見える。

```
$ ccsession preview cccccccc-...
started : 2099-01-01 00:00  (just now)
```

`Relative` 内のクランプは妥当だが、ヘッダ表記では `(?)` か
`(in the future)` のような明示が望ましい。

#### B-6: ラベル経由で ANSI / 制御文字が素通り

セッション本文に `\x1b[31mRED\x1b[0m` や `\x07` (BEL) が含まれていると、
`sanitizeLabel` (`parse.go:184`) はタブ / CR / LF / 連続スペースしか
潰さないので **ESC や BEL がそのまま fzf に渡って描画される**。

```
$ ccsession list --no-color | grep proj-ansi | od -c | head -1
... t   p   r   o   j   -   a   n   s   i  \t 033   [   3   1   m   R   E   D 033 ...
```

自身の transcript なので攻撃シナリオは弱いが、誤って色付き貼り付け
した内容を含むセッションが fzf の見た目を破壊しうる。
修正案: `sanitizeLabel` で `\x00-\x1F` (タブだけは別扱い) と DEL を
スペースに置換する。

#### B-9: `[gone]` マーカが label 列に付いていて見落としやすい

`internal/list/list.go:83` の format は
`"%s\t%d\t%s\t%s\t%s%s"` (id / epoch / rel / basename / marker+label) で、
fzf の `--with-nth=3,4,5` の見た目だと cwd 喪失セッションは **basename
カラム (col 4) は普通のまま**で、ラベル列の頭にだけ `[gone]` が付く。
basename だけ眺めているユーザには気づかない可能性が高い。

修正案: basename 自体を黄色に着色するか、`[gone]` を basename 側に寄せる。

#### B-10: `cwd` 欠落時のディレクトリ名フォールバックが lossy

`restoreCWDFromDir` (`parse.go:217`) はプロジェクトディレクトリ名の
ハイフンを `/` に戻すので、元 cwd の **コンポーネントに含まれる `-` が
パスの境界に化ける**。

```
-home-foo-bar-proj → /home/foo/bar/proj
```

このセッションは表示上 basename `proj` (実存しないので `[gone]`) で、
resume すると `original cwd is gone` で失敗する。ユーザにとっては
「なぜか壊れたセッション」に見える。

修正案: cwd 欠落セッションには `(cwd unknown)` を出してフォールバックを
信用させない、もしくは `<projectdir>/.cwd` 的なメタファイルがあれば
読む。

### Low

#### B-11: preview の `bufio.Scanner` バッファが 4 MiB 固定

`preview.go:148` は `scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)`。
1 行が 4 MiB を超える transcript (巨大なツール出力など) で Scanner は
途中で打ち切られるが、現在のコードは `scanner.Err()` を return するだけ
で**それまでに描画したメッセージは既に出力されている**ため、ユーザは
"途中まで描画されてエラーが出た" 状態を見る。バッファサイズを大きくする
か、`bufio.Reader` ベースに置き換える。

#### B-12: `list` 既定が常に ANSI カラー出力

`ccsession list` を他のコマンドにパイプすると `\x1b[36m...` 込みの TSV が
そのまま流れる。`--no-color` フラグはあるが、stdout が TTY でない場合の
自動 off があると親切。

#### B-13: 未知のサブコマンド時、ヘルプが stderr に出る

`main.go:92` で `fmt.Fprint(os.Stderr, usage)`。exit code は 2 で正しい
が、usage を stderr に流すと `ccsession xyz 2>/dev/null` で完全に消える。
慣例的には usage は stdout。

#### B-14: `flag.ExitOnError` の Usage 文が CLI 全体のスタイルから浮く

`ccsession list --bogus` で出るのは Go の flag パッケージ既定の
`Usage of list:` ブロック。`ccsession --help` の手書き usage と
スタイルが揃わない。

#### Observation: テストが 1 件もない

`go test ./...` は全パッケージ `no test files`。最低限 `timefmt.Relative`,
`session.sanitizeLabel`, `session.restoreCWDFromDir`, `session.ParseSessionTail`
あたりはユニットテストの効果が大きい (純関数で副作用がない)。

## 安全だったケース (= 期待どおりだった挙動)

- `--grep` のクエリにシェルメタ文字 (`; touch /tmp/pwned`) を渡しても
  `exec.Command` 経由なので発火しない。
- `preview ../etc/passwd` のような相対パス id は `filepath.Join` でクリーン
  され、`~/.claude/projects/` 配下に閉じる。
- `agent-*.jsonl` 除外、空ファイル除外、user-message なしセッションの
  除外は意図通り動作。
- 壊れた JSON 行 (`broken json`) はスキップされ、同一ファイルの正常な行
  だけが採用される。
- 200 rune (UTF-8 multibyte 含む) のラベル truncate は rune 単位で正確
  (200 runes / 600 バイトの `あ` 入力で 200 runes に切り詰め + `…`)。
- 並列スキャン (`runtime.NumCPU()*2`) で 15 件規模のフィクスチャは即返る。
- 未知のサブコマンド・引数不足・`--help`・`--version` は妥当な exit code
  を返す。

## 修正の優先順位 (筆者意見)

1. **B-1 (空白ラベル素通し)** と **B-7 (--grep 構造漏れ)** は出力を直接
   信用できなくする系なので最優先。B-1 は数行で直る。B-7 はパースベース
   実装に置き換えるなら工数がそれなりに掛かるので、暫定的に README で
   「JSONL バイト検索」と明記するだけでも可。
2. **B-2 (未来日時で sort 暴れ)** と **B-3 ("not found" 誤表示)** は
   一行レベルで直せて UX が大きく改善するので次点。
3. **B-6 (ANSI 素通し)** はセキュリティというより堅牢性の問題。
   `sanitizeLabel` に制御文字置換を 1 行足すだけ。
4. **B-8 (rg regex エラー埋もれ)** は `-F` 既定 + opt-in で `--regex`
   フラグを追加するのが綺麗。
5. テスト 0 件 → 純関数からでも追加開始したい。

## 再現用フィクスチャ

実検証に使ったフィクスチャ生成スクリプトは [`./fixtures/mkfixtures.sh`](./fixtures/mkfixtures.sh)
を参照。`HOME=/tmp/fakehome` を被せて `ccsession list` などを実行する。
