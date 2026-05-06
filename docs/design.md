# port-peeker 設計書

LB のヘルスチェックを HTTP で受けて、ホスト上のポート LISTEN 状態とプロセスの生存を確認して 200/503 を返すワンバイナリ HTTP サーバ。

本書は現状の実装に即した設計書である。実装されていない将来機能は [roadmap.md](roadmap.md) を参照。重要な設計判断の経緯は [decisions/](decisions/) を参照。

## 1. 背景と目的

### 1.1 課題

LB (NLB / ALB / HAProxy 等) のヘルスチェックには次の課題がある:

- **TCP ヘルスチェックは死活判定が荒い**: ポートが LISTEN していれば healthy 扱い。プロセスがゾンビ化してもポートさえ開いていれば検知できない。
- **プロトコル別ヘルスチェックは限定的**: NLB は TCP/UDP/TLS/HTTP のみ。SMTP/IMAP/POP3 などの軽量プロトコルチェックは対応していない。
- **メールサービス等で実プロトコルにヘルスチェックを当てると、ヘルスチェック由来のログが大量に出る**: 認証失敗ログや切断ログがノイズになる。
- **複数サービスを 1 ホストで動かしている場合、それぞれの死活を細かく分けたい**: Postfix / Dovecot / nginx などが同居するホストで、サービス単位の UNHEALTHY 判定が欲しい。

### 1.2 目的

- LB のヘルスチェックを HTTP リクエスト 1 つに集約
- クエリパラメータで「どのポートのどのプロセスを確認したいか」を指定可能
- ホスト上のポート LISTEN 状態 + プロセス情報を確認して 200/503 を返す
- ワンバイナリで配布・管理が簡単
- ヘルスチェック先を実サービスに当てないことで、サービスログをクリーンに保つ

### 1.3 ターゲット環境

- Linux (`/proc` を直接読むため Linux 専用)
- AWS NLB / ALB / HAProxy など、HTTP ヘルスチェックを発行する任意の LB
- メールサーバ、複数サービス同居サーバ等

## 2. 機能仕様

### 2.1 提供するエンドポイント

#### `GET /check`

クエリパラメータで指定された条件を満たすか確認する。

**クエリパラメータ**:

| 名前 | 型 | 必須 | 説明 |
|---|---|---|---|
| `port` | int (1-65535) | はい | LISTEN しているか確認するポート番号 |
| `process` | string | いいえ | LISTEN プロセスの名前 (例: `dovecot`, `master`) |

**レスポンス**:

すべて `Content-Type: text/plain; charset=utf-8` で末尾に改行が付く。

| 状態 | コード | ボディ |
|---|---|---|
| 全条件 OK | 200 | `OK\n` |
| ポートが LISTEN していない | 503 | `port N not listening\n` |
| プロセス名が不一致 | 503 | `process mismatch (expected X, got Y)\n` |
| 内部エラー (procfs 読み取り失敗等) | 503 | `check error: ...\n` |
| `port` パラメータ欠落 | 400 | `missing port parameter\n` |
| `port` パラメータ不正 | 400 | `invalid port: ...\n` |

`Y` には検出したプロセス名のカンマ区切り、または検出できなかった場合は `(none)` が入る。一般ユーザで起動した場合、自プロセス以外の `/proc/<pid>/fd` を読めないため、他人プロセスは `(none)` 扱いになる。

#### `GET /healthz`

エージェント自体の死活確認。常に 200、ボディは `OK\n`。LB が「エージェント自身が動いているか」を確認する用途。

### 2.2 起動オプション

設定ファイルは持たず、起動引数のみで動作する (シンプルさ優先)。

```
port-peeker [options]

Options:
  --listen ADDR         HTTP 待ち受けアドレス (host:port) [default ":24365"]
  --cache-ttl DURATION  /check 結果のキャッシュ TTL; 0 で無効 [default 5s]
  --version             バージョンを表示して終了
  --help                ヘルプを表示して終了
```

PROXY Protocol v1/v2 ヘッダは接続ごとに自動検出される (フラグ不要、§4.4 参照)。

引数なしで実行した場合は `--help` と同じヘルプ表示で終了する。

複数サービスを使い分ける場合は LB 側のヘルスチェックパスで `/check?port=...` を細かく指定する。

## 3. アーキテクチャ

### 3.1 構成

```
[LB Health Check]
       │ HTTP GET /check?port=993&process=dovecot
       ↓
[port-peeker on :24365]
       │
       ├─→ /proc/net/tcp{,6} を 1 回パースして port=993 の LISTEN 行 (st=0A) の inode 集合を得る
       │     - inode 集合が空でなければ「LISTEN 中」
       │
       └─→ process=dovecot 指定時のみ:
             /proc/<pid>/fd/* を walk し socket:[INODE] が inode 集合に含まれる pid を見つけて
             /proc/<pid>/comm でプロセス名を取得
       │
       ↓
[200 OK / 503 / 400]
```

### 3.2 チェックロジック

#### Port check

`/proc/net/tcp` と `/proc/net/tcp6` の各行で:

- `st` フィールド (4 列目) が `0A` (LISTEN) かを確認
- `local_address` (2 列目) の `:` 以降のポート部分を 16 進数で照合
- 一致したら `inode` フィールド (10 列目) を集合に追加

`inode` 集合が空でなければ、その port は LISTEN 中。`net.DialTimeout` 等の TCP 接続は行わない (対象サーバにヘルスチェック由来の接続ログを発生させないため)。

#### Process check

`process` パラメータが指定された場合のみ実行。

1. `/proc/<pid>/` の各エントリ (数字のみのディレクトリ) を走査
2. `/proc/<pid>/fd/*` の readlink を見て `socket:[INODE]` 形式かつ inode が上で得た集合に含まれるかを確認
3. 一致した pid について `/proc/<pid>/comm` を読みプロセス名を得る
4. 取得したプロセス名のいずれかが `process` パラメータと一致すれば 200。一致しなければ 503

権限エラー (他ユーザの `/proc/<pid>/fd` の readlink 不可) は無視して次の pid に進む。これは設計上の挙動であり、結果として「自プロセス以外解決できない一般ユーザ起動時は `(none)` で mismatch」となる。

### 3.3 キャッシュ

LB のヘルスチェックは秒単位で頻繁に来るため、毎回 `/proc` 全走査するのは無駄。同一クエリの結果を `--cache-ttl` 秒間 (デフォルト 5 秒) キャッシュする。

- キャッシュキー: `port|process` 形式の文字列 (例: `993|dovecot`、`80|`)
- 値: HTTP ステータス + レスポンスボディ (`handler.Result`)
- 実装: `sync.RWMutex + map[string]entry[T]` のジェネリックなキャッシュ。Go の generics を使用
- `--cache-ttl 0` で完全無効化 (`Get` は常にミス、`Set` は no-op)
- TTL 過ぎたエントリは Get 時に miss 扱い (能動的な eviction はしない)

### 3.4 並行性

- HTTP サーバは Go の `net/http` 標準実装で、ゴルーチンによる並行処理
- 同時実行数の制限は設けない (LB のヘルスチェックは数十/秒以下が一般的)
- `Checker` は不変 (mutable state を持たない) ため lock 不要
- `Cache` は `sync.RWMutex` で保護

## 4. 実装

### 4.1 言語と依存

**Go** を採用。HTTP server / CLI / `/proc` パーサ / キャッシュ等のコアロジックは標準ライブラリのみ。PROXY Protocol サポートだけは [`github.com/pires/go-proxyproto`](https://github.com/pires/go-proxyproto) を利用 (詳細は §4.4 と [decisions/DR-005-proxy-protocol-v2-support.md](decisions/DR-005-proxy-protocol-v2-support.md))。`CGO_ENABLED=0` で純 Go バイナリとしてクロスビルド可能。

採用理由の詳細は [decisions/DR-001-language-go.md](decisions/DR-001-language-go.md) と [decisions/DR-003-zero-dependency.md](decisions/DR-003-zero-dependency.md) を参照。

### 4.2 ディレクトリ構成

```
port-peeker/
├── cmd/
│   └── port-peeker/
│       └── main.go              # エントリーポイント、引数パース、HTTP サーバ起動
├── internal/
│   ├── checker/
│   │   └── checker.go           # /proc/net/tcp{,6} と /proc/<pid>/ の inspect 実装
│   ├── cache/
│   │   └── cache.go             # ジェネリックな TTL キャッシュ
│   └── handler/
│       └── check.go             # /check と /healthz のハンドラ
├── docs/                        # 本ドキュメント群
├── go.mod
├── justfile
└── README.md
```

### 4.3 主要コードの責務

#### `cmd/port-peeker/main.go`

- フラグパース (`flag` パッケージ、Usage は自前差し替え)
- 引数なしで実行された場合のヘルプ表示
- `Checker` と `Cache` の初期化
- `http.ServeMux` への `/check` と `/healthz` の登録
- `http.Server` の起動 (`ReadHeaderTimeout: 5s`)
- SIGTERM / SIGINT を受けて 5 秒タイムアウトのグレースフルシャットダウン
- バージョンは `var version = "dev"` を `-ldflags "-X main.version=..."` で上書き

#### `internal/checker/checker.go`

```go
type Checker struct {
    NetFiles []string  // 既定: ["/proc/net/tcp", "/proc/net/tcp6"]
    ProcRoot string    // 既定: "/proc"
}

type Status struct {
    Listening bool
    Processes []string
}

func New() *Checker
func (c *Checker) Inspect(port int, wantProcesses bool) (Status, error)
```

`Inspect` は port LISTEN 判定とプロセス名解決を 1 メソッドに統合している。`/proc/net/tcp` を 1 回パースすれば LISTEN 判定 (inode 集合が空かどうか) と process 解決の起点 (inode 集合) が同時に得られるため。

`NetFiles` と `ProcRoot` はテスト時に差し替え可能なフィールドとして公開している。

詳細は [decisions/DR-002-proc-direct-no-external-cmd.md](decisions/DR-002-proc-direct-no-external-cmd.md)、[decisions/DR-004-inspector-unified.md](decisions/DR-004-inspector-unified.md) を参照。

#### `internal/cache/cache.go`

```go
type Cache[T any] struct { /* ... */ }
func New[T any](ttl time.Duration) *Cache[T]
func (c *Cache[T]) Get(key string) (T, bool)
func (c *Cache[T]) Set(key string, v T)
```

Go 1.18+ の generics を使い、任意の型をキャッシュできる汎用 TTL キャッシュ。`ttl <= 0` のときは Get 常時 miss / Set no-op。

#### `internal/handler/check.go`

```go
type Inspector interface {
    Inspect(port int, wantProcesses bool) (checker.Status, error)
}

type Result struct {
    Status int
    Body   string
}

type Check struct {
    Insp  Inspector
    Cache *cache.Cache[Result]
}

func (h *Check) ServeHTTP(w http.ResponseWriter, r *http.Request)
func Healthz(w http.ResponseWriter, _ *http.Request)
```

ハンドラは `Inspector` インターフェース越しに `Checker` を呼ぶ (テスト容易性のため)。クエリパラメータ検証 → キャッシュ参照 → `Inspect` → 結果判定 → キャッシュ保存 → 応答書き込み、の流れ。

## 4.4 PROXY Protocol

LB が PROXY Protocol を有効にしている場合、各 TCP 接続の先頭にクライアント情報を伝えるヘッダが付与される。port-peeker は [`github.com/pires/go-proxyproto`](https://github.com/pires/go-proxyproto) の `Listener` を経由して接続を受けることで v1/v2 ヘッダを自動検出して剥がし、実クライアント IP を `RemoteAddr()` に反映する。設定フラグは無く、無効化する手段も提供しない (詳細は [decisions/DR-005-proxy-protocol-v2-support.md](decisions/DR-005-proxy-protocol-v2-support.md))。

ライブラリの USE policy (デフォルト) を採用しているため:

- v1 (テキスト) と v2 (バイナリ) の両方を自動検出
- TCP/IPv4 と TCP/IPv6 の PROXY コマンドをパースして addr を反映
- v2 LOCAL コマンドや v1 `UNKNOWN` はヘッダだけ消費して原 RemoteAddr を保持
- ヘッダが無い接続は素の TCP としてそのまま通過

`cmd/port-peeker/main.go` が `&proxyproto.Listener{Listener: rawLn}` を `http.Server.Serve` に渡すだけ。

## 5. デプロイ

### 5.1 ビルド

ホスト向け:

```sh
just build
# → bin/port-peeker
```

Linux 向けクロスビルド:

```sh
just build-linux
# → bin/port-peeker-linux-amd64
# → bin/port-peeker-linux-arm64
```

`CGO_ENABLED=0` + `-buildvcs=false` を指定。`-buildvcs=false` は jj 管理下の git bare 構成で `go build` の VCS スタンプ取得が失敗するための回避策。

### 5.2 起動

```sh
port-peeker --listen :24365
```

### 5.3 systemd で常駐化

リポジトリ同梱の `systemd/port-peeker.service` を `/etc/systemd/system/` に配置して `systemctl enable --now` するだけで常駐化できる。

```sh
# Linux ターゲットのバイナリを配置
sudo install -m 755 bin/port-peeker-linux-arm64 /usr/local/bin/port-peeker

# unit ファイルを配置 (リポジトリ内のサンプルをそのまま使う)
sudo install -m 644 systemd/port-peeker.service /etc/systemd/system/port-peeker.service

sudo systemctl daemon-reload
sudo systemctl enable --now port-peeker

# 動作確認
curl -s http://127.0.0.1:24365/healthz
sudo systemctl status port-peeker
journalctl -u port-peeker -f
```

unit ファイルのデフォルトは `User=root` で全機能 (process 名解決を含む) が動く構成。`?process=NAME` を使わず port LISTEN 判定のみで運用する場合は `DynamicUser=yes` に切り替えることでサンドボックスを強化できる (具体的な切替方法は unit ファイル内のコメント参照)。

PROXY Protocol v1/v2 は自動検出されるため、NLB の `proxy_protocol_v2 = ON` でもプレーン HTTP でも追加設定なしでそのまま動く。

サンドボックス指定 (`NoNewPrivileges` / `ProtectSystem=strict` / `ProtectHome` / `ProtectKernelTunables` / `MemoryDenyWriteExecute` 等) はデフォルトで有効。port-peeker は `/proc` を読み HTTP を返すだけなので、これらを有効にしても動作に支障はない。

### 5.4 LB 設定例

ALB / NLB ターゲットグループのヘルスチェック:

```
プロトコル: HTTP
ポート: 24365
パス: /check?port=993&process=dovecot
正常コード: 200
インターバル: 30s
タイムアウト: 5s
正常閾値: 3
非正常閾値: 3
```

ターゲットグループごとにパスを変えて使い分ける:

| ターゲットグループ | ヘルスチェックパス |
|---|---|
| smtp (25) | `/check?port=25&process=master` |
| smtps (465) | `/check?port=465&process=master` |
| submission (587) | `/check?port=587&process=master` |
| imaps (993) | `/check?port=993&process=dovecot` |
| pop3s (995) | `/check?port=995&process=dovecot` |
| imap (143) | `/check?port=143&process=dovecot` |
| pop3 (110) | `/check?port=110&process=dovecot` |

## 6. 非機能要件

### 6.1 パフォーマンス

- 1 リクエストあたり処理時間: キャッシュヒット < 1ms、ミス時は `/proc/net/tcp` の行数 + `/proc/<pid>/fd` の総数に依存 (通常数 ms - 数十 ms)
- メモリ使用量: 想定 10MB 以下 (Go ランタイム + 小さなキャッシュ)
- 同時接続数: 100 程度を想定

### 6.2 信頼性

- HTTP サーバは標準の `net/http` で、`ReadHeaderTimeout: 5s` を設定
- procfs 読み取りエラーは 503 + `check error: ...` で応答
- SIGTERM / SIGINT で 5 秒タイムアウトのグレースフルシャットダウン

### 6.3 セキュリティ

- LISTEN は VPC 内のみを想定 (LB からの接続のみ)
- 認証なし (LB との通信路を SG で守る前提)
- ログにクエリ内容 (port / process) を出すが、機密情報は含まれない設計
- 攻撃面を最小化: `/check` と `/healthz` の 2 経路のみ

### 6.4 運用性

- ログは標準出力 / 標準エラーに出す (journald は `Type=simple` で標準出力を捕捉)
- バージョン情報を `--version` で表示
- ヘルプを `--help` および引数なし起動で表示

## 7. 既存ツールとの比較

| ツール | 軽量さ | プロセス確認 | サービスログを汚さない | LB 連携の容易さ |
|---|---|---|---|---|
| **port-peeker** | ◎ ワンバイナリ・依存なし | ◎ 標準対応 | ◎ TCP 接続を発生させない | ◎ HTTP API |
| monit | △ 設定重い | ◎ | △ | △ HTTP I/F あるが LB 向きでない |
| consul | × 重量級 | ◎ | △ | ○ HTTP API |
| systemd の `systemd-notify` | ◎ | ◎ | ◎ | × LB は systemd を直接見れない |
| TCP ヘルスチェック (素) | ◎ | × | × 接続ログが出る | ◎ |

ニッチだが確実に需要のある領域。

## 8. 制約事項

- Linux 専用 (`/proc/net/tcp` および `/proc/<pid>/` を読む)
- BSD / macOS / Windows は対象外
- 他人プロセスのプロセス名解決には対象プロセスと同 UID または root 権限が必要。一般ユーザーで起動した場合、自プロセス以外は `(none)` 扱いになり、`process=` 指定は事実上 root か対象プロセスと同 UID での起動を要求する
- IPv4 / IPv6 双方の LISTEN を見る (`/proc/net/tcp` と `/proc/net/tcp6` の両方)
- コンテナ環境では `/proc/net/tcp` がコンテナ単位になるため、ホスト側のヘルスチェックには使えない

## 9. ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)

## 付録: 想定する利用シーン

### A. メールサーバ (本設計の発端)

NLB + Postfix + Dovecot 構成で、ヘルスチェック由来のログを実サービスに飛ばさず、各サービスの死活を分離して監視。

### B. 複数 Web アプリの同居

1 ホストで nginx + アプリ A + アプリ B が動いている場合、それぞれのプロセス・ポート単位で分離してヘルスチェック。

### C. ステートフルサービス

Redis / MySQL / PostgreSQL のように、TCP 接続が成功してもプロセスがハングしている可能性があるサービスで、プロセス名一致を組み合わせて確認 (より深いプロトコルチェックは [roadmap.md](roadmap.md) 参照)。

### D. デバッグ・運用調査

`curl http://localhost:24365/check?port=993&process=dovecot` で手動確認できるので、運用調査時にもそのまま流用可能。
