#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  printf 'usage: TARGET_DATABASE_URL=postgres://... %s <backup.dump>\n' "$0" >&2
  exit 2
fi
: "${TARGET_DATABASE_URL:?TARGET_DATABASE_URL is required}"
backup=$1
if [ ! -r "$backup" ]; then
  printf 'backup is not readable: %s\n' "$backup" >&2
  exit 2
fi

pg_restore --clean --if-exists --no-owner --no-privileges --exit-on-error --dbname="$TARGET_DATABASE_URL" "$backup"
printf '%s\n' 'restore completed'
