#!/bin/sh
set -eu
top=$(git rev-parse --show-toplevel)
cd "$top"
dash=$(printf '\342\200\224')
status=0
hits=$(git grep -I -n -F -e "$dash" -- \
  ':(exclude,glob)**/node_modules/**' \
  ':(exclude,glob)**/package-lock.json' \
  ':(exclude,glob)**/go.sum' \
  ':(exclude,glob)**/.terraform.lock.hcl') || status=$?
case $status in
  0)
    printf '%s\n' "$hits" | sed 's/^\([^:]*:[0-9]*\):/\1: /'
    echo "Replace each em dash with a new sentence or a comma."
    exit 1
    ;;
  1)
    echo "No em dash in any tracked text file."
    ;;
  *)
    echo "check-prose: git grep failed with exit $status." >&2
    exit "$status"
    ;;
esac
