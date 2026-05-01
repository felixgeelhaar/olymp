#!/usr/bin/env bash
# Bootstrap one logical database per cognitive-stack service. The `olymp`
# user owns every database for demo simplicity; production deploys should
# use per-service roles.

set -euo pipefail

if [ -z "${POSTGRES_MULTIPLE_DATABASES:-}" ]; then
  exit 0
fi

DBS=(${POSTGRES_MULTIPLE_DATABASES//,/ })

for db in "${DBS[@]}"; do
  if [ "$db" = "${POSTGRES_DB:-}" ]; then
    continue
  fi
  echo "Creating database: $db"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<EOSQL
SELECT 'CREATE DATABASE "$db"'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\gexec
EOSQL
done
