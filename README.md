# port-peeker

LB のヘルスチェックを HTTP で受けて、ホスト上のポート LISTEN 状態とプロセスの生存を確認して 200/503 を返すワンバイナリ HTTP サーバ。

`/proc` を直接読むため、対象サービスにヘルスチェック由来のログを発生させない。`ss` などの外部コマンドにも依存しない。

## 使い方

```sh
# 起動
port-peeker --listen :24365

# LB から:
curl -s -o /dev/null -w '%{http_code}\n' \
  'http://127.0.0.1:24365/check?port=993&process=dovecot'
# → 200 (LISTEN かつプロセス名一致) / 503 (それ以外) / 400 (パラメータ不正)
```

## エンドポイント

| Path | 用途 |
|---|---|
| `GET /check?port=N[&process=NAME]` | ホスト上で port が LISTEN しているか（任意で process 名一致も）確認 |
| `GET /healthz` | エージェント自身の死活確認（常に 200） |

## オプション

```
--listen ADDR         待ち受けアドレス (default ":24365")
--cache-ttl DURATION  チェック結果キャッシュの TTL; 0 で無効 (default 5s)
--version             バージョンを表示して終了
--help                ヘルプ表示
```

引数なしで実行した場合も `--help` と同じ表示になる。

PROXY Protocol v1/v2 ヘッダは接続ごとに自動検出されるため、NLB の `proxy_protocol_v2 = ON` 経由でもプレーン HTTP でも追加設定なしで動作する。

## 必要環境

- Linux (`/proc/net/tcp`, `/proc/net/tcp6`, `/proc/<pid>/fd`, `/proc/<pid>/comm` を読む)
- 他人プロセスのプロセス名解決には対象プロセスと同 UID か root 権限が必要 (一般ユーザで起動した場合、自プロセス以外は `(none)` 扱いになる)

## ビルド

```sh
just build           # ホスト向け
just build-linux     # Linux amd64 + arm64
```

## systemd で常駐化

リポジトリ同梱の [`systemd/port-peeker.service`](systemd/port-peeker.service) を使う。

```sh
sudo install -m 755 bin/port-peeker-linux-arm64 /usr/local/bin/port-peeker
sudo install -m 644 systemd/port-peeker.service /etc/systemd/system/port-peeker.service
sudo systemctl daemon-reload
sudo systemctl enable --now port-peeker
curl -s http://127.0.0.1:24365/healthz
```

詳細は [docs/design.md §5.3](docs/design.md) を参照。

## ドキュメント

- [docs/design.md](docs/design.md) — 設計書
- [docs/roadmap.md](docs/roadmap.md) — 将来計画
- [docs/dr/](docs/dr/) — Design Record (重要な設計判断の記録)

## ライセンス

MIT
