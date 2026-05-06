# port-peeker

# デフォルト: レシピ一覧
default:
    @just --list

# ホスト向けビルド (-buildvcs=false: jj+git-bare 構成で VCS スタンプ取得が失敗する回避策)
build:
    go build -buildvcs=false -o bin/port-peeker ./cmd/port-peeker

# Linux 向けクロスビルド (CGO_ENABLED=0 で純 Go バイナリ)
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

# VERSION を bump して Release commit を push (CI が tag + GitHub Release を作成)
bump-version bump="patch": ensure-clean check test
    #!/usr/bin/env bash
    set -euo pipefail

    # VERSION 変更が main に push されると release.yml が検出して
    # tag (v$VERSION) と GitHub Releases を自動作成する。tag を人が打つ必要はない。

    current=$(cat VERSION | tr -d '[:space:]')
    IFS='.' read -r major minor patchv <<< "$current"
    case "{{bump}}" in
        major) major=$((major + 1)); minor=0; patchv=0 ;;
        minor) minor=$((minor + 1)); patchv=0 ;;
        patch) patchv=$((patchv + 1)) ;;
        *) echo "Error: invalid bump '{{bump}}'" >&2; exit 1 ;;
    esac
    new_version="${major}.${minor}.${patchv}"
    echo "Version: ${current} -> ${new_version}"

    # @ は空 change (ensure-clean で確認済)。VERSION を書き換えて Release commit に
    printf '%s\n' "${new_version}" > VERSION
    jj describe -m "Release v${new_version}"
    jj new

    # push (release.yml がここから走る)
    just push

    # release.yml を watch
    sleep 3
    run_id=$(gh run list --repo kawaz/port-peeker --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')
    gh run watch "$run_id" --repo kawaz/port-peeker
