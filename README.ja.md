# gprt

[English](README.md) | 日本語

GitHub のプルリクエストをターミナルで扱うための TUI です。一覧の閲覧からレビュー、コメント、マージまで、vim 風のキー操作だけで完結します。

## 機能

- **すべてのプルリクエストを 1 つの一覧に**: 複数リポジトリをまたいで、自分(とチーム)へのレビュー依頼、自分の PR、関わっている PR、さらに任意の検索条件をセクションとして表示します。`/` で絞り込み、open / closed / merged を切り替えられます。
- **PR ビュー**: 説明文、ラベル、レビュアー、CI チェック、会話のタイムライン全体を表示します。
- **Files ビュー**: ファイルツリーとシンタックスハイライト付きの diff。レビュースレッドは diff 内にインライン表示され、折りたためます。
- **レビュー**: 行・範囲・ファイル単位のコメント、返信、スレッドの解決。コメントを即時投稿するか、ペンディングレビューに溜めて Approve / Request changes / Comment として提出するかを選べます。
- **リアクション**と、リポジトリのユーザーによる `@` メンション補完。
- **編集とライフサイクル**: タイトル、説明文、base ブランチ、ラベル、レビュアー、ドラフト状態の編集。TUI からの PR 作成。`:merge`、`:close`、`:reopen` によるマージ・クローズ・再オープン(いずれも確認ダイアログ付き)。
- **vim 風エディタ**で文章を書き、`:e` で `$EDITOR` に切り替えられます。下書きは入力のたびに保存され、再起動後も残ります。
- **速くて静か**: `gh` のログイン情報を再利用し、API レスポンスをディスクにキャッシュし、バックグラウンドで更新します。操作キーはすべて設定で変更できます。

## インストール

必要なもの:

- ログイン済みの [`gh` CLI](https://cli.github.com/)(`gh auth login`)、または環境変数 `GH_TOKEN` / `GITHUB_TOKEN`。GitHub Enterprise Server は `gh` のホスト設定を通じて利用できます。
- ソースからインストールする場合は Go 1.26 以上。

Linux / macOS / Windows(amd64、arm64)向けのビルド済みバイナリを各[リリース](https://github.com/hirano00o/gprt/releases)に添付しています。お使いのプラットフォームのアーカイブをダウンロードし、`gprt` を `PATH` の通った場所に置いてください。

Nix(flakes 有効)の場合:

```sh
nix run github:hirano00o/gprt          # インストールせずに試す
nix profile add github:hirano00o/gprt  # プロファイルにインストール
```

NixOS や Home Manager の設定で使うには、このリポジトリを flake の input に追加し、`inputs.gprt.packages.<system>.default` を参照してください。`github:hirano00o/gprt/v0.1.0` のようにタグやコミットを末尾に付けるとバージョンを固定できます。

ソースからインストールする場合:

```sh
go install github.com/hirano00o/gprt/cmd/gprt@latest
```

チェックアウトからビルドする場合:

```sh
git clone https://github.com/hirano00o/gprt.git
cd gprt
go build ./cmd/gprt
```

## 使い方

```sh
gprt
```

左の一覧に自分のプルリクエストが表示されます。`j`/`k` で移動、`Enter` で開き、`gt`/`gT` で **PR** タブと **Files** タブを切り替えます。`?` でキー一覧、`:` でコマンド入力、`q` で終了です。

| フラグ | 効果 |
|--------|------|
| `--config <path>` | デフォルトの `config.yaml` の代わりにこのファイルを使う |
| `--debug` | `~/.local/state/gprt/gprt.log` にデバッグログを書く(`GPRT_DEBUG=1` でも可) |
| `--version` | バージョンを表示して終了する |

## 設定

設定は任意です。`gprt` は `~/.config/gprt/config.yaml`(または `$XDG_CONFIG_HOME/gprt/config.yaml`。`GPRT_CONFIG_DIR` でディレクトリを上書きできます)を読み込みます。すべてのキーは省略できます。

```yaml
host: github.com          # GitHub host; defaults to gh's default host
refresh_interval: 5m      # background refresh of the list and the open PR
icons: unicode            # unicode | nerd (Nerd Font glyphs)
editor: ""                # command for :e; defaults to $EDITOR, then vim
browser: ""               # command for o; defaults to $BROWSER, then the OS default
highlight_style: github-dark  # any chroma style name
tab_width: 4
list:
  state: open             # open | closed | merged | all
  sections:               # extra search sections, appended to the built-in ones
    - name: Backend
      query: "org:acme label:backend"
keys:                     # remap any action (vim notation); see docs/KEYBINDINGS.md for the IDs
  global.toggle_list: "<C-w>o"
  diff.comment: "c"
  pr.submit: "S"
```

`editor` と `browser` はシェルのクォート規則で分割され、`$VAR` と `~` は展開されます(glob は展開されません)。`GPRT_BROWSER` は `browser` の設定より優先されます。

そのほかのディレクトリ: API レスポンスのキャッシュは `~/.cache/gprt`、下書きとログは `~/.local/state/gprt` に置かれます。

## キーバインド

エディタ自身のキーを除き、以下はすべて設定で変更できます。アクション ID を含む完全な一覧は [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md)(英語)にあります。

### 全般

| キー | 動作 |
|------|------|
| `j` / `k`, `gg` / `G`, `Ctrl-d` / `Ctrl-u` | 下 / 上、先頭 / 末尾、半ページ移動 |
| `gt` / `gT`(`Ctrl-l` / `Ctrl-h` も可) | 次 / 前のタブ |
| `Ctrl-w h` / `Ctrl-w l` | 前 / 次のペインにフォーカス |
| `Ctrl-w o` | PR 一覧の表示 / 非表示 |
| `o` | 現在の項目をブラウザで開く |
| `R` | キャッシュを無視して再読み込み |
| `:` | コマンド入力: `:merge`、`:close`、`:reopen`、`:reload`、`:messages`、`:help`、`:q` |
| `?` | ヘルプ |
| `q`, `Ctrl-c` | 開いているダイアログを閉じる、または終了 |

### PR 一覧

| キー | 動作 |
|------|------|
| `Enter`, `l` | プルリクエストを開く |
| `/` | 一覧を絞り込む(`Esc` で解除) |
| `n` | プルリクエストを作成 |

### PR タブ

| キー | 動作 |
|------|------|
| `c` | 新しいコメント |
| `e` / `d` | 自分のコメントを編集 / 削除。説明文の上で `e` を押すと説明文を編集 |
| `a` | リアクションの追加 / 削除 |
| `E` | タイトル、base ブランチ、ラベル、レビュアー、ドラフト状態を編集 |
| `S` | レビューを提出: Approve / Request changes / Comment |
| `p` | ペンディング中のレビューコメントと保存済みの下書き |

### Files タブ

| キー | 動作 |
|------|------|
| `Enter`, `l` | カーソル位置のファイルを開く(ツリー内) |
| `Ctrl-w t` | ファイルツリーの表示 / 非表示 |
| `V` | 範囲コメント用に行を選択(`Esc` で取り消し) |
| `c` | 行または選択範囲にコメント。スレッド上では返信 |
| `C` | ファイル全体にコメント |
| `r` | スレッドに返信 |
| `x` | スレッドを解決 / 未解決に戻す |
| `e` / `d` | 自分のレビューコメントを編集 / 削除 |
| `a` | リアクションの追加 / 削除 |
| `za` / `zR` / `zM` | スレッドを折りたたむ / すべて展開 / すべて折りたたむ |
| `]c` / `[c` | 次 / 前のスレッド |
| `]f` / `[f` | 次 / 前のファイル |
| `zh` / `zl` | 水平スクロール |

`E`、`S`、`p` はここでも使えます。

### エディタ

| キー | 動作 |
|------|------|
| `i` `a` `I` `A` `o` `O` | インサートモードに入る |
| `Esc` | ノーマルモードに戻る |
| `h` `j` `k` `l` `w` `b` `e` `0` `^` `$` `gg` `G` | 移動(カウント指定可) |
| `x` `dd` `dw` `cc` `cw` `yy` `p` `P` `J` … | 削除、変更、ヤンク、貼り付け、行の結合 |
| `u` / `Ctrl-r` | 元に戻す / やり直す |
| `v` / `V` | ビジュアル / 行ビジュアル選択 |
| `@` | メンション補完: `Ctrl-n` / `Ctrl-p` で移動、`Tab` または `Enter` で確定 |
| `:w`, `Ctrl-s` | 送信 |
| `:q` / `:q!` | 下書きを残して / 破棄して閉じる |
| `:e` | `$EDITOR` で本文を編集 |

行・範囲・ファイルへのコメントを送信すると、GitHub と同じく「単一コメントとして投稿」か「ペンディングレビューに追加」かを選べます。確認ダイアログは **Cancel** が初期選択なので、実行するには `Tab` を押してから `Enter` を押します。

## 貢献

バグ報告やプルリクエストを歓迎します。

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
```

アーキテクチャは [docs/DESIGN.md](docs/DESIGN.md)、機能一覧と実装状況は [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)、全キーとアクション ID は [docs/KEYBINDINGS.md](docs/KEYBINDINGS.md) にまとめています(いずれも英語)。

## ライセンス

MIT。[LICENSE](LICENSE) を参照してください。
