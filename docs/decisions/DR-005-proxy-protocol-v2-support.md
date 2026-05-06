# DR-005: PROXY Protocol を内蔵サポート

- ステータス: Accepted
- 日付: 2026-05-07

## Context

NLB のターゲットグループ属性 `proxy_protocol_v2 = true` を有効にすると、NLB がバックエンドへ TCP 接続するたびに、最初に PROXY Protocol v2 のバイナリヘッダ (12 byte シグネチャ + 16 byte 固定ヘッダ + 可変アドレス) を送り、その後で本来のペイロード (HTTP リクエスト) が続く。HAProxy / Nginx など他の LB は v1 (テキスト形式) を出すこともある。

メールサーバ用途では「実クライアント IP をバックエンドから見たい」という要件のため `proxy_protocol_v2 = ON` が必要だが、port-peeker は標準の `net/http` で listen しているだけだと PROXY ヘッダを HTTP リクエストとしてパースしようとして 400 を返してしまい、ヘルスチェックが恒常的に失敗する。

代替の運用上の回避策:

- ヘルスチェック用に別ターゲットグループを作って `proxy_protocol_v2 = OFF` にする → ターゲットグループ二重化が必要で大改修
- port-peeker 用に listener を分けて `proxy_protocol_v2 = OFF` の listener でヘルスチェックを受ける → NLB 構成複雑化
- 一時的に `proxy_protocol_v2 = OFF` に戻す → クライアント IP が backend から見えなくなる本来要件を満たさない

## Decision

port-peeker は `http.Server.Serve` に渡す listener を [`github.com/pires/go-proxyproto`](https://github.com/pires/go-proxyproto) でラップする (USE policy = デフォルト)。これにより接続ごとに v1/v2 ヘッダを自動検出し、ヘッダ有りなら剥がして実クライアント IP を `RemoteAddr()` に反映、ヘッダ無しなら素の TCP として通す。設定フラグは設けない。

実装はわずかに 1 行:

```go
ln := &proxyproto.Listener{Listener: rawLn}
http.Server.Serve(ln)
```

サポート範囲は USE policy が標準でカバー:

- v1 (テキスト) と v2 (バイナリ) の両方
- TCP/IPv4 と TCP/IPv6 の PROXY コマンドでアドレス反映
- v2 LOCAL コマンド / v1 `UNKNOWN` / その他の family/proto はヘッダだけ消費して原 RemoteAddr を保持

## Rationale

- **要件成立**: NLB の `proxy_protocol_v2 = ON` のままヘルスチェックが通る
- **設定不要**: ユーザが LB の有無や種別を意識する必要がなく、誤設定起因のトラブルが構造的に発生しない
- **悪用の余地が無い**: PROXY ヘッダは「クライアントから見た自分の IP を申告する」だけで、port-peeker の判定ロジック (`/check`, `/healthz`) には影響しない。ログに出る実 IP が偽装される程度のリスクで、port-peeker は SG 内で LB のみから到達する前提のためそもそも問題にならない
- **v1 も対応**: NLB は v2 だが HAProxy / Nginx は v1 を出すケースもある。両方の現場でそのまま動かせる
- **メンテナンスの委譲**: PROXY Protocol は仕様自体は単純だが、コーナーケース (TLV 拡張, AF_UNSPEC, ヘッダの早期切断, ssl-info の TLV 等) を含めると実装維持コストが効いてくる。`go-proxyproto` は HAProxy 公式仕様の参照実装の 1 つで、メンテナンスが活発。自前で持たないことでこれらの面倒を移譲できる
- **責務単一**: listener wrapper として閉じており、ハンドラ側のロジックには影響しない

[DR-003](DR-003-zero-dependency.md) (HTTP server / CLI コアは標準ライブラリのみ) との関係: 本ツールの判定ロジックや HTTP / CLI 部分は引き続きゼロ依存を維持する。listener レイヤーの周辺機能だけ「十分にメンテされた小さいライブラリは許容」という運用にする。

## Alternatives Considered

- **自前実装 (旧案)**: v1+v2 両対応でも 200 行ほどで書けるが、コーナーケースの追従や仕様拡張のメンテコストを自プロジェクトで持つ必要がある。実際に最初は自前で実装したが、`go-proxyproto` で十分置き換えられることを確認したうえで切り替えた
- **v2 のみサポート**: NLB だけが対象なら成立するが、HAProxy / Nginx の v1 を考えると将来手戻りが発生する
- **`--proxy-protocol disable|auto|require` でモード切替 (旧案)**: 柔軟性は得られるが、本ツールの用途では設定の必要性がない。誤設定で 400 を返したり LB 経由でないと拒否したりする運用事故の余地を作るだけで、デメリットが上回ると判断
- **ヘルスチェック専用にターゲットグループや listener を分離**: NLB 側の構成が複雑化し、運用コスト増
- **`proxy_protocol_v2 = OFF` で運用**: クライアント IP が backend から見えなくなる本来要件を満たさない
