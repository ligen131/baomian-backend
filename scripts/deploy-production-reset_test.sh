#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$repo/scripts/deploy-production-reset.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if "$script" >"$tmp/no-confirm.out" 2>&1; then
  printf '%s\n' 'deploy script unexpectedly accepted missing confirmation' >&2
  exit 1
fi
grep -q -- '--confirm RESET-PRODUCTION' "$tmp/no-confirm.out"

"$script" --help >"$tmp/help.out" 2>&1
grep -q -- '--confirm RESET-PRODUCTION' "$tmp/help.out"

if "$script" --confirm WRONG >"$tmp/wrong-confirm.out" 2>&1; then
  printf '%s\n' 'deploy script unexpectedly accepted wrong confirmation' >&2
  exit 1
fi
grep -q 'exact confirmation RESET-PRODUCTION is required' "$tmp/wrong-confirm.out"

python3 - "$script" <<'PY'
import re
import sys
from pathlib import Path

text = Path(sys.argv[1]).read_text()
if re.search(r'psql\b.*?\s-c\s+".*?:\x27user_id\x27', text, re.S):
    raise SystemExit('deploy script uses psql -c with a psql variable; variables are not expanded in -c SQL')
PY

printf '%s\n' 'deploy-production-reset test passed'
