#!/bin/sh
# Role untuk Debezium (CDC, mata kuliah Data Engineering). Dijalankan entrypoint postgres
# sekali saat volume baru. Hak: LOGIN + REPLICATION, baca tabel di database bank dan orders.
# Tabel dibuat belakangan oleh service (user sister), jadi hak baca diberikan lewat
# ALTER DEFAULT PRIVILEGES. Publication dbz_bank / dbz_orders dibuat oleh service bank / order.
set -eu
U="${DEBEZIUM_USER:-debezium}"
P="${DEBEZIUM_PASSWORD:-changeme-debezium}"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -v u="$U" -v p="$P" <<'SQL'
CREATE ROLE :"u" WITH LOGIN REPLICATION PASSWORD :'p';
GRANT CONNECT ON DATABASE bank, orders TO :"u";
SQL
for db in bank orders; do
  psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$db" -v u="$U" -v owner="$POSTGRES_USER" <<'SQL'
GRANT USAGE ON SCHEMA public TO :"u";
GRANT SELECT ON ALL TABLES IN SCHEMA public TO :"u";
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public GRANT SELECT ON TABLES TO :"u";
SQL
done
