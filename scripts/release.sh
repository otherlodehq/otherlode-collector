#!/usr/bin/env bash
# Cuts a collector release from Luke's machine (ADR 0006):
#
#   scripts/release.sh X.Y.Z [--dry-run]
#
# Every check runs before any change. The script then commits `const version = "X.Y.Z"`, tags
# `vX.Y.Z`, commits the next SNAPSHOT and pushes the branch and the tag in one atomic push; the tag
# starts the release workflow. `--dry-run` stops after the checks. Needs git and go.
set -euo pipefail

VERSION_FILE='cmd/otherlode-collector/version.go'
COLLECTOR_REPO='otherlodehq/otherlode-collector'

die() {
  echo "release.sh: $*" >&2
  exit 1
}

# Rewrites the version constant in version.go and checks that the module still builds.
set_version() { # version
  sed -i.bak -E "s/^const version = \".*\"\$/const version = \"$1\"/" "$VERSION_FILE" && rm "$VERSION_FILE.bak"
  [ "$(sed -n 's/^const version = "\(.*\)"$/\1/p' "$VERSION_FILE")" = "$1" ] || die "could not set $VERSION_FILE to $1"
  go build ./... || die "the module does not build with version $1. To undo: git checkout -- $VERSION_FILE"
  git add "$VERSION_FILE"
}

main() {
  local version='' dry_run=0 arg
  for arg in "$@"; do
    case "$arg" in
      --dry-run) dry_run=1 ;;
      -*) die "unknown option $arg" ;;
      *)
        [ -z "$version" ] || die "more than one version given"
        version=$arg
        ;;
    esac
  done
  [[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    die "usage: scripts/release.sh X.Y.Z [--dry-run] (digits only, no leading zeros)"
  local major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
  local tag="v$version" next="$major.$minor.$((patch + 1))-SNAPSHOT"

  cd "$(git rev-parse --show-toplevel)"
  local tool
  for tool in git go; do command -v "$tool" >/dev/null || die "$tool is not installed"; done

  [ "$(git rev-parse --abbrev-ref HEAD)" = master ] || die "not on master"
  [ -z "$(git status --porcelain --untracked-files=all)" ] || die "the working tree is not clean (tracked or untracked changes)"
  git fetch --quiet --tags origin
  [ "$(git rev-parse master)" = "$(git rev-parse origin/master)" ] || die "local master differs from origin/master: pull or push first"
  if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then die "tag $tag already exists locally"; fi
  if [ -n "$(git ls-remote --tags origin "refs/tags/$tag")" ]; then die "tag $tag already exists on origin"; fi
  local declared last
  declared=$(sed -n 's/^const version = "\(.*\)"$/\1/p' "$VERSION_FILE")
  [ "$declared" = "$version-SNAPSHOT" ] ||
    die "$VERSION_FILE declares '$declared'; releasing $version needs $version-SNAPSHOT there first"
  last=$(git tag --list 'v*.*.*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1 || true)
  if [ -n "$last" ] && [ "$(printf '%s\n%s\n' "${last#v}" "$version" | sort -V | tail -n 1)" != "$version" ]; then
    die "$version is not above the last release, $last"
  fi

  [ -f CHANGELOG.md ] || die "CHANGELOG.md is missing"
  local section
  section=$(scripts/changelog-section.sh "$version")
  [[ "$section" =~ [^[:space:]] ]] || die "CHANGELOG.md has no non-empty '## [$version]' section"

  go build ./... || die "the module does not build at HEAD"

  if [ "$dry_run" -eq 1 ]; then
    cat <<EOT
Dry run: no file, commit or tag changed (the fetch updated remote-tracking refs). Without --dry-run this would:
  - commit version "$version" in $VERSION_FILE as "Release $version"
  - tag $tag ("otherlode-collector $version") on that commit
  - commit version "$next" as "Begin ${next%-SNAPSHOT}"
  - git push --atomic origin master $tag
EOT
    return 0
  fi

  set_version "$version"
  git commit --quiet -m "Release $version"
  git tag -a "$tag" -m "otherlode-collector $version"
  set_version "$next"
  git commit --quiet -m "Begin ${next%-SNAPSHOT}"
  if ! git push --atomic origin master "$tag"; then
    if [ -n "$(git ls-remote --tags origin "refs/tags/$tag" 2>/dev/null)" ]; then
      die "the push reported a failure but $tag is on origin, so it went through: check the release workflow, and do not reset"
    fi
    die "the push failed and $tag is not on origin. To undo locally: git tag -d $tag && git reset --hard origin/master"
  fi

  cat <<EOT

Pushed $tag. The release workflow runs at:
  https://github.com/$COLLECTOR_REPO/actions/workflows/release.yml
The image is pushed to ghcr.io/otherlodehq/otherlode-collector. A package GitHub creates on the first push
may start private, while the GitHub release is published at once: check the package's visibility under the
organisation's packages before announcing the release.
EOT
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
