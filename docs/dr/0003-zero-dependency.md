# DR-0003: HTTP server / CLI ともに標準ライブラリのみを使用

- ステータス: Accepted
- 日付: 2026-05-06

## Context

port-peeker は配布性とビルド安定性を重視する小型ツール。HTTP ルーティング (`/check`、`/healthz`) とコマンドライン引数パースをどう実装するかを決める必要があった。Go エコシステムには `chi` / `gin` / `echo` などの HTTP ルータ、`pflag` / `cobra` / `urfave/cli` などの引数パーサが揃っている。

kawaz の CLI 設計ルール (`~/.claude/rules/cli-design-preferences.md`) は以下を要求している:

- ロングオプションを基本とする (`--listen` 等、`--flag` 表記)
- 引数なしで実行時は `--help` を表示
- ヘルプ出力をセクション分けして表示

これを満たせるかどうかが選定基準になった。

## Decision

`net/http` と `flag` を標準のままで使う。**外部依存はゼロ**。CLI ヘルプは `flag.Usage` を差し替えて自前整形する。

`go.mod` の `require` ブロックは空 (Go 標準ライブラリのみ)。

## Rationale

- **ワンバイナリ・依存管理不要**: 外部依存ゼロにすることで、`go.sum` の管理コスト、依存の脆弱性追従、依存パッケージのバージョン互換問題のいずれも発生しない。
- **`flag` パッケージで CLI ルールを満たせる**:
  - `flag.String("listen", ...)`、`flag.Duration("cache-ttl", ...)` で `--listen ADDR`、`--cache-ttl DURATION` のロングオプション形式が使える (Go の `flag` は `-listen` も `--listen` も両方受け付ける)
  - `flag.Usage` を関数で差し替えれば、セクション分けや独自整形が自由にできる
  - `len(os.Args) == 1` 判定で「引数なしで起動 → help 表示」を実装できる
- **`net/http` で十分**: ルーティングは `/check` と `/healthz` の 2 経路のみ。`http.ServeMux` の `mux.Handle("/check", h)` / `mux.HandleFunc("/healthz", f)` で完結する。ミドルウェアやパスパラメータも不要。
- **ビルドの安定性**: 標準ライブラリ縛りなら Go バージョンを上げてもほぼ壊れない。CI ビルドの再現性が高い。

## Alternatives Considered

- **`chi` / `gin` / `echo` 等の HTTP ルータ**: パスパラメータ、ミドルウェアチェーン、グループ化など、port-peeker では不要な機能が多い。2 経路の static path しかない現状では `http.ServeMux` で十分。
- **`pflag` / `cobra` / `urfave/cli`**: `pflag` は POSIX 準拠の `--flag` 表記をより厳密にサポートし、`cobra` / `urfave/cli` はサブコマンド構造を提供する。port-peeker はサブコマンドを持たない単純な常駐 CLI で、`flag` パッケージでも `--flag` 形式は受け付けられるため、追加依存の利得が薄い。`flag.Usage` を差し替える前提なら、ヘルプ整形のために pflag/cobra を入れる必要もない。
