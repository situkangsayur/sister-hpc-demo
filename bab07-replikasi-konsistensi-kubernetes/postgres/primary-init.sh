#!/bin/sh
# Dijalankan sekali oleh image postgres saat primary pertama kali dibuat.
# Membuat role replication, replication slot, dan izin pg_hba untuk replica.
set -e
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPL_PASSWORD}';
SELECT pg_create_physical_replication_slot('replica1');
SQL
echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
