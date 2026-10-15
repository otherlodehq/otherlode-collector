#!/bin/sh
set -eu
src=${1:?usage: check-shared.sh <path to an otherlode-agent checkout>}
drift=0
for path in $(grep -v -e '^#' -e '^$' "$src/.claude/shared-files"); do
  if ! diff -r "$src/$path" "$path" >/dev/null 2>&1; then
    echo "differs from otherlode-agent: $path"
    drift=1
  fi
done
if [ "$drift" -ne 0 ]; then
  echo "Copy these paths from otherlode-agent's master."
  exit 1
fi
echo "Every shared path matches otherlode-agent."
