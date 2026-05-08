# DR-006: サービス毎の専用ヘルスチェックポートではなくホスト単位の汎用エージェントを採用

- ステータス: Accepted
- 日付: 2026-05-07

## Context

port-peeker のそもそもの発端は、メールサーバ (Postfix + Dovecot) を NLB 配下で運用していたときに発生していたヘルスチェック由来のログノイズ問題である。NLB の TCP ヘルスチェックが各サービスポート (110, 143, 993, 995, 25, 465, 587 など) に直接当たることで、Dovecot は接続毎に `pop3-login: Disconnected: Connection closed (no auth attempts in 0 secs)` を記録し、Postfix postscreen も同様の繰り返しログを出していた。NLB はクロスゾーンで複数 ENI から数秒間隔で来るため、ログ全体の大半がヘルスチェック起源のノイズで埋まり、本物の異常やセキュリティイベントが見えにくくなっていた。

問題のスコープ:

- 「ノイズが多いと困る」のは運用視点の品質問題であり、機能不全ではない
- 一方でログが見えないことで本物の異常検知が遅れるリスクは現実的
- 既存ツール (monit / consul / 商用 APM) は重量級で、メールサーバホストに対しては過剰

## Decision

各サービス (Postfix / Dovecot / 将来追加されるサービス) ごとにヘルスチェック専用の listener を生やすのではなく、**ホスト単位で 1 つの汎用 HTTP ヘルスチェックエージェント (= port-peeker)** を立ててそこに NLB のヘルスチェックを集約する。

- NLB ターゲットグループは `target port = 実サービスポート、health check port = 24365 (port-peeker)、path = /check?port=PORT&process=NAME` の形で振り分ける
- port-peeker は `/proc` を読むだけで TCP 接続を発生させないため、対象サービスにヘルスチェック由来の接続ログが一切出ない

## Rationale

- **拡張性**: 新しいサービス (例: 別ポートで動く別デーモン) を追加するときに必要なのは LB 側で `/check?port=...&process=...` のパスを 1 行足すことだけで、サービスのコンフィグや syslog 設定を触る必要がない
- **構造的にログが汚れない**: rsyslog フィルタや syslog_name 分離のような後付けの抑制策ではなく、そもそも対象サービスに接続を発生させない。「ノイズを後から消す」ではなく「ノイズを発生させない」アプローチ
- **認知負荷が累積しない**: サービス毎にヘルスチェック専用 listener とフィルタを足していくと、サーバ全体の構成が「本来のサービス設定 + ヘルスチェック対応の特設設定」の二層構造になる。サービスが増えるほど「これは何のための設定か」を読み解く負担が増える。port-peeker に集約すれば、各サービスの設定ファイルにはヘルスチェック関連の追記が不要
- **OS 依存からの解放**: rsyslog 前提のフィルタ案は Amazon Linux 2023 のように journald のみの環境では使えない (補足は [design.md §6.4](../design.md))

## Alternatives Considered

- **Postfix `master.cf` にヘルスチェック専用 smtpd (例: `10025`)**: `syslog_name=postfix/healthcheck` で別プログラム名にして rsyslog でフィルタする案。サービス単位では成立するが、Dovecot 用にも別途立てる必要があり、サービス毎に設定が増える。`mynetworks` 等の relay 設定との整合も常に意識しないと open relay リスクを生む
- **Dovecot に専用 inet_listener (例: `port=10110`)**: 上と同じく個別対応。`service pop3-login` 内に `inet_listener pop3-healthcheck { port = 10110, haproxy = no, ssl = no }` を追加する形で実装できるが、これも Postfix とは独立して維持する必要がある
- **rsyslog フィルタ (`:msg, contains, "no auth attempts in 0 secs" stop` 等)**: ノイズの「表示を消す」だけで、対象サービスへの不要 TCP 接続は依然として発生する。Amazon Linux 2023 では rsyslog がデフォルトで未インストールで、入れると journald との二重記録になるため、「ノイズを消すために新たな冗長系統を立てる」本末転倒になりがち
- **NLB のヘルスチェック頻度を下げる**: 対症療法。クロスゾーン構成では完全には消えず、ヘルスチェック判定のレイテンシも悪化する
- **Consul / monit 等の既存重量級ツール**: 機能は満たすがオーバースペック。常駐エージェント + UI + 設定言語の学習コストが用途に対して大きすぎる
