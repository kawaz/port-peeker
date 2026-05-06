# DR-0004: PortChecker と ProcessChecker を Inspector 1 個に統合

- ステータス: Accepted
- 日付: 2026-05-06

## Context

設計書たたき台では `internal/checker/` 配下に `port.go` / `process.go` / `unit.go` / `proto.go` と機能別にファイルを分け、それぞれインターフェースを切る形だった:

```go
type PortChecker interface {
    IsListening(port int) (bool, error)
}

type ProcessChecker interface {
    ProcessNamesFor(port int) ([]string, error)
}
```

port-peeker の MVP では `port` 判定と `process` 名解決の 2 つだけがチェック対象。これを別インターフェースに分けるかを決める必要があった。

## Decision

`checker.Checker` という単一の構造体に `Inspect(port int, wantProcesses bool) (Status, error)` のみを公開する。port LISTEN 判定とプロセス名解決を 1 メソッドに統合する。

```go
type Status struct {
    Listening bool
    Processes []string
}

func (c *Checker) Inspect(port int, wantProcesses bool) (Status, error)
```

ハンドラ側は `Inspector` インターフェースで `*checker.Checker` を抽象化する (テスト容易性のため)。

```go
type Inspector interface {
    Inspect(port int, wantProcesses bool) (checker.Status, error)
}
```

## Rationale

- **`/proc/net/tcp` の重複読み込みを避ける**: LISTEN 判定とプロセス解決は、どちらも `/proc/net/tcp{,6}` のパース結果 (= 該当 port の inode 集合) を起点とする。
  - `Listening = (len(inodes) > 0)` で LISTEN 判定が済む
  - 同じ inode 集合を `/proc/<pid>/fd` の readlink と突き合わせれば process が解決できる
  - `PortChecker.IsListening` と `ProcessChecker.ProcessNamesFor` を別 I/F にすると、それぞれが独立に `/proc/net/tcp` を読むことになり、1 リクエストで 2 回スキャンする無駄が生じる。
- **責務分離より読み込みコスト**: 「単一責務原則的に分けるべき」という観点はあるが、本ツールでは `/proc` 読み込みコストの方が支配的なので、責務分離の利得より読み込み削減の利得が上回る。
- **API がシンプル**: ハンドラは `wantProcesses := processName != ""` を渡すだけで、process パラメータが省略された場合は `/proc/<pid>/fd` の walk もスキップできる。条件分岐が `Inspect` 内に閉じ込められる。
- **キャッシュキーが単純**: 統合した結果 (`Status`) を 1 つキャッシュすればよい。port 用と process 用で別キャッシュを持つ必要がない。

## Alternatives Considered

- **PortChecker と ProcessChecker を別 I/F**: ドメインモデル的には綺麗 (port 確認とプロセス確認は概念的に別の関心事)。だが上記の通り `/proc/net/tcp` を 2 回読むことになり、I/O コストが倍。本ツールの性能要件 (LB ヘルスチェック秒数十回) を考えると、概念的整理よりコストを優先したい。将来 `unit` チェックや `proto` チェックを追加する場合は、それぞれ別の責務 (systemd D-Bus / TCP 接続) なので別 I/F として分離する想定。
- **inode 集合を返す中間 API + 別 I/F の組み合わせ**: `c.ListenInodes(port)` を共通の前段にして、`c.IsListening(inodes)` / `c.ProcessNamesFor(inodes)` のように分ける案。理屈としては綺麗だが、ハンドラ側が中間状態 (inodes) を握って引き回す必要があり、API の使い勝手が悪化する。「正しく使える」設計より「正しくしか使えない」設計を取って `Inspect` 1 個に閉じる方を選んだ。
