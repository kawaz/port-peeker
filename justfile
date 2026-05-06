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

# リリース (bump: patch | minor | major)
# 最新の v* tag から bump して @- に tag を打って origin に push する。
# GitHub Actions の release.yml が tag を検出して linux/amd64 と linux/arm64
# バイナリをビルドし GitHub Releases に添付する。
release bump="patch": ensure-clean check test
    #!/usr/bin/env bash
    set -euo pipefail

    git_dir="$(jj root)/../.git"
    latest=$(git --git-dir="$git_dir" tag --list 'v*' --sort=-v:refname | head -1)
    if [ -z "$latest" ]; then
        new_tag="v0.1.0"
        echo "First release: $new_tag"
    else
        IFS='.' read -r major minor patchv <<< "${latest#v}"
        case "{{bump}}" in
            major) major=$((major + 1)); minor=0; patchv=0 ;;
            minor) minor=$((minor + 1)); patchv=0 ;;
            patch) patchv=$((patchv + 1)) ;;
            *) echo "Error: invalid bump '{{bump}}'" >&2; exit 1 ;;
        esac
        new_tag="v${major}.${minor}.${patchv}"
        echo "Release: $latest -> $new_tag"
    fi

    # main bookmark を @- に進めて push (main commit を origin に反映)
    jj bookmark set main -r @-
    jj git push --bookmark main

    # @- に tag を打って push (release.yml がここから走る)
    jj tag set "$new_tag" -r @-
    jj git export
    git --git-dir="$git_dir" push origin "$new_tag"

    # release.yml を watch
    sleep 3
    run_id=$(gh run list --repo kawaz/port-peeker --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')
    gh run watch "$run_id" --repo kawaz/port-peeker
