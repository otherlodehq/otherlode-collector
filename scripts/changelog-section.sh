#!/usr/bin/env bash
# Prints the body of a changelog's `## [X.Y.Z]` section:
#
#   scripts/changelog-section.sh X.Y.Z [CHANGELOG.md]
#
# The body ends at the next `## [` heading or at the first link reference definition
# (`[0.1.0]: https://...`), which Keep a Changelog puts at the bottom of the file, inside the oldest
# section. The heading is matched literally, so 0.1.0 never matches 0.1.01 or 10.1.0. Used by
# scripts/release.sh and by the release workflow, so both read the same section.
set -euo pipefail

[ $# -ge 1 ] || {
  echo "usage: scripts/changelog-section.sh X.Y.Z [CHANGELOG.md]" >&2
  exit 2
}

awk -v v="$1" '
  /^## \[/ {
    if (in_section) exit
    if (index($0, "## [" v "]") == 1) { in_section = 1; next }
  }
  /^\[[^]]+\]:/ { if (in_section) exit }
  in_section { print }
' "${2:-CHANGELOG.md}"
