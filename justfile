# port-peeker

# デフォルト: レシピ一覧
default:
    @just --list

# ビルド (host)
# Note: -buildvcs=false は jj 管理下の git bare 構成で `go build` の VCS
# スタンプ取得が失敗するための回避策。
build:
    go build -buildvcs=false -o bin/port-peeker ./cmd/port-peeker

# Linux 向けクロスビルド (amd64 + arm64)
# CGO_ENABLED=0: 純 Go バイナリで cross-compile（macOS から Linux clang へ通すのを避ける）
build-linux:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -o bin/port-peeker-linux-amd64 ./cmd/port-peeker
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -o bin/port-peeker-linux-arm64 ./cmd/port-peeker

# テスト
test:
    go test ./...

# lint + format チェック
check:
    test -z "$(gofmt -l .)" || { echo "gofmt suggests changes:" >&2; gofmt -d .; exit 1; }
    go vet ./...

# format 適用
fmt:
    gofmt -w .

# ビルドして実行
run *ARGS: build
    ./bin/port-peeker {{ARGS}}

# ワーキングコピーがクリーン（empty）であることを確認
ensure-clean:
    test "$(jj log -r @ --no-graph -T 'empty')" = "true"

# push (ensure-clean + check + test を通してから @- を push)
push: ensure-clean check test
    jj bookmark set main -r @-
    jj git push --bookmark main
