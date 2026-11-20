#!/bin/sh
# Entrypoint replica: bila data directory kosong, salin primary dengan pg_basebackup,
# tulis primary_conninfo (application_name=replica1 untuk synchronous_standby_names),
# lalu jalankan postgres sebagai hot standby.
set -e
mkdir -p "$PGDATA"
chown postgres:postgres "$PGDATA"
chmod 700 "$PGDATA"
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "replica: menunggu primary..."
  until su-exec postgres pg_isready -h pg-primary -U postgres -q; do sleep 1; done
  sleep 2
  echo "replica: pg_basebackup dari primary"
  until PGPASSWORD="$REPL_PASSWORD" su-exec postgres pg_basebackup -h pg-primary -U replicator \
      -D "$PGDATA" -Fp -Xs -c fast --no-sync -S replica1 -w; do
    echo "replica: pg_basebackup gagal, coba lagi"; rm -rf "${PGDATA:?}"/*; sleep 2
  done
  cat >> "$PGDATA/postgresql.auto.conf" <<CONF
primary_conninfo = 'host=pg-primary port=5432 user=replicator password=${REPL_PASSWORD} application_name=replica1'
primary_slot_name = 'replica1'
hot_standby = on
hot_standby_feedback = on
recovery_min_apply_delay = '${REPLICA_APPLY_DELAY_MS:-0}ms'
CONF
  su-exec postgres touch "$PGDATA/standby.signal"
  chown postgres:postgres "$PGDATA/postgresql.auto.conf"
fi
# fsync=off hanya untuk demo: replica dapat dibangun ulang dari primary kapan saja.
exec su-exec postgres postgres -c fsync=off
