# `justfile::bump-version` を `bump-semver` に移行 + `ci` レシピ + `just ci` 1 行 CI

## 背景

2026-05-09 に `kawaz/bump-semver` v0.2.0 がリリース済 (`brew install kawaz/tap/bump-semver`)。`Cargo.toml` / `*.json` / `VERSION` を basename で自動判定する flat 4-action CLI。

port-peeker は **bump-semver の設計のお手本** (VERSION ファイル駆動 + matrix build + `--generate-notes` の最小骨格として横断調査で参照された) で、自身も移行対象。現在の `bump-version` レシピは `printf '%s\n' "${new}" > VERSION` で直書きしている。これを `bump-semver` に揃える (一貫性のため)。

## やること

### 1. ローカル PATH に `bump-semver` を入れる前提条件

`kawaz/dotfiles/darwin/default.nix` の `homebrew.brews` に `"kawaz/tap/bump-semver"` を追加 (別 issue: `kawaz/dotfiles/docs/issue/2026-05-09-add-bump-semver-to-homebrew-brews.md`)。

### 2. `bump-version` レシピを `bump-semver` に置換 + 改名

現状 (port-peeker/main/justfile 抜粋):

```just
bump-version bump="patch": ensure-clean check test
    #!/usr/bin/env bash
    set -euo pipefail
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
    printf '%s\n' "${new_version}" > VERSION
    jj describe -m "Release v${new_version}"
    jj new
    just push
    sleep 3
    run_id=$(gh run list --repo kawaz/port-peeker --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')
    gh run watch "$run_id" --repo kawaz/port-peeker
```

移行後 (推奨形):

```just
# レシピ名は呼び出すツール名と揃えて bump-semver に統一 (kawaz リポ全体で同じパターン)
# 引数名は level (semver bump level の慣用)
bump-semver level="patch": ensure-clean check test
    #!/usr/bin/env bash
    set -euo pipefail
    new_version=$(bump-semver "{{level}}" VERSION --write)
    echo "Version: -> ${new_version}"
    jj describe -m "Release v${new_version}"
    jj new
    just push
    sleep 3
    run_id=$(gh run list --repo kawaz/port-peeker --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')
    gh run watch "$run_id" --repo kawaz/port-peeker
```

13 行 → 4 行。bump ロジック (case 文 + printf) を `bump-semver` に閉じ込める。

### 3. `ci` レシピ + `.github/workflows/ci.yml` の `just ci` 1 行化 (もし未対応なら)

```just
ci: check test build
```

```yaml
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
      - uses: extractions/setup-just@v3
      - run: just ci
```

### 4. 自己循環参照に注意

port-peeker 自身が `kawaz/bump-semver` のリリース fork 元のひとつなので、bump-semver への依存がぐるりと回ることになる。リスクは小さい (bump-semver が壊れても port-peeker のコードは動き、bump-semver の修正は kawaz/bump-semver 側で行えばよい) が、以下のフォールバック手順を README/docs に書いておくと安全:

> bump-semver が利用できない場合は手動で `VERSION` を書き換えて `jj describe -m "Release v..."` → `just push` で同等の効果が得られる。

## 想定される作業順序

1. dotfiles の brew install 追加が完了していることを確認
2. `which bump-semver` で確認
3. justfile の `bump-version` を `bump-semver` レシピに置換 + リネーム
4. CI workflow を `just ci` 1 行に集約 (該当する場合)
5. push → CI 緑確認

## 関連

- bump-semver: https://github.com/kawaz/bump-semver (v0.2.0)
- 先行適用例: `kawaz/jj-worktree/main/justfile` (commit `ba9add89` 以降)
- ルール: `~/.claude/rules/docs-structure.md` の「バージョン bump レシピ」節

報告者: kawaz/jj-worktree main の親 CC (session_id: `718c6cc3-b154-4de5-9cbe-cccd6dcfa407`) — 2026-05-09
