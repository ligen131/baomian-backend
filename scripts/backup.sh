#!/usr/bin/env sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
BACKUP_DIR=${BACKUP_DIR:-.local/backups}
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
target="$BACKUP_DIR/baomian-$timestamp.dump"
umask 077
pg_dump --format=custom --no-owner --no-privileges --file="$target" "$DATABASE_URL"
chmod 600 "$target"
printf '%s\n' "$target"
