#!/usr/bin/env bash
# Uji otomatis shared/observability:
#   1) nyalakan Grafana + Loki + Alloy,
#   2) jalankan satu container uji yang menulis log JSON (berlabel compose project sister-obs-uji),
#   3) cek log masuk Loki lewat API (label compose_project, compose_service, container, level,
#      service, dan structured metadata request_id),
#   4) cek Grafana: health, datasource Loki, dashboard "Log per demo", akses anonymous viewer,
#   5) matikan dan bersihkan (selalu, lewat trap).
# Bila stack sudah menyala sebelum uji dimulai (dipakai demo lain), stack dibiarkan menyala.
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
BIND_ADDR=${BIND_ADDR:-127.0.0.1}
GRAFANA=http://$BIND_ADDR:50000
LOKI=http://$BIND_ADDR:50001
ALLOY=http://$BIND_ADDR:50002
UJI_ID="uji-$(date +%s)-$RANDOM"
UJI_CT="sister-obs-uji-$RANDOM"
SUDAH_JALAN=0
if [ -n "$(docker compose ps -q 2>/dev/null)" ]; then SUDAH_JALAN=1; fi

lolos=0; gagal=0
ok()   { echo "  [LOLOS] $*"; lolos=$((lolos+1)); }
salah(){ echo "  [GAGAL] $*"; gagal=$((gagal+1)); }

bersih() {
  docker rm -f "$UJI_CT" >/dev/null 2>&1 || true
  if [ "$SUDAH_JALAN" = 0 ]; then
    echo "== Membersihkan (down -v)"
    docker compose down -v --remove-orphans >/dev/null 2>&1 || true
  else
    echo "== Stack sudah menyala sebelum uji; dibiarkan menyala."
  fi
}
trap bersih EXIT

tunggu() { # tunggu URL pola maks-detik
  local url=$1 pola=$2 maks=${3:-90} i
  for ((i=0; i<maks; i++)); do
    if curl -fsS --max-time 3 "$url" 2>/dev/null | grep -q "$pola"; then return 0; fi
    sleep 1
  done
  return 1
}

echo "== df -h /"; df -h / | tail -1
echo "== Menyalakan sister-obs"
docker compose up -d --wait --wait-timeout 600

tunggu "$LOKI/ready" ready 90 && ok "Loki ready" || salah "Loki tidak ready"
tunggu "$GRAFANA/api/health" ok 90 && ok "Grafana health ok" || salah "Grafana tidak sehat"
tunggu "$ALLOY/-/ready" "ready" 90 && ok "Alloy ready (UI $ALLOY)" || salah "Alloy tidak ready"

echo "== Menjalankan container uji $UJI_CT (request_id=$UJI_ID)"
docker run -d --name "$UJI_CT" \
  --label com.docker.compose.project=sister-obs-uji \
  --label com.docker.compose.service=penulis-log \
  --log-driver json-file --log-opt max-size=1m \
  alpine:3.20 sh -c "i=0; while [ \$i -lt 60 ]; do
      echo '{\"time\":\"'\$(date -u +%Y-%m-%dT%H:%M:%SZ)'\",\"level\":\"INFO\",\"msg\":\"halo dari uji\",\"service\":\"penulis-log\",\"request_id\":\"$UJI_ID\",\"i\":'\$i'}';
      echo '{\"level\":\"ERROR\",\"msg\":\"contoh error\",\"service\":\"penulis-log\",\"request_id\":\"$UJI_ID\"}';
      i=\$((i+1)); sleep 1; done" >/dev/null

echo "== Menunggu log masuk Loki"
Q='{compose_project="sister-obs-uji", compose_service="penulis-log", level="info", service="penulis-log"} | request_id="'$UJI_ID'"'
hasil=""
for ((i=0; i<60; i++)); do
  hasil=$(curl -fsS -G "$LOKI/loki/api/v1/query_range" \
    --data-urlencode "query=$Q" --data-urlencode "since=10m" --data-urlencode "limit=20" 2>/dev/null || true)
  if echo "$hasil" | grep -q "halo dari uji"; then break; fi
  sleep 2
done
if echo "$hasil" | grep -q "halo dari uji"; then
  ok "log JSON container uji terbaca di Loki (filter label + request_id)"
else
  salah "log container uji tidak ditemukan di Loki"; echo "$hasil" | head -c 600; echo
fi
echo "$hasil" | grep -q "\"container\":\"$UJI_CT\"" && ok "label container=$UJI_CT ada" || salah "label container tidak ada"

err=$(curl -fsS -G "$LOKI/loki/api/v1/query_range" \
  --data-urlencode 'query={compose_project="sister-obs-uji", level="error"}' --data-urlencode "since=10m" 2>/dev/null || true)
echo "$err" | grep -q "contoh error" && ok "level ERROR dinormalisasi menjadi label level=error" || salah "label level=error tidak ditemukan"

lbl=$(curl -fsS "$LOKI/loki/api/v1/labels" 2>/dev/null || true)
for l in compose_project compose_service container level service; do
  echo "$lbl" | grep -q "\"$l\"" && ok "label $l terdaftar di Loki" || salah "label $l tidak terdaftar"
done

echo "== Grafana"
ds=$(curl -fsS "$GRAFANA/api/datasources/uid/loki" -u "admin:$GRAFANA_ADMIN_PASSWORD" 2>/dev/null || true)
echo "$ds" | grep -q '"type":"loki"' && ok "datasource Loki terprovisioning (login admin dari .env)" || salah "datasource Loki tidak ada"
dash=$(curl -fsS "$GRAFANA/api/dashboards/uid/sister-log-per-demo" 2>/dev/null || true)
echo "$dash" | grep -q '"title":"Log per demo"' && ok "dashboard 'Log per demo' terbaca oleh anonymous viewer" || salah "dashboard tidak terbaca anonymous"
echo "$dash" | grep -q '"folderTitle":"Sister Demo"' && ok "dashboard ada di folder 'Sister Demo'" || salah "folder dashboard salah"
# Dashboard milik bab (dipasang dari babNN-*/grafana lewat provider kedua).
for duid in sister-bab01 sister-bab05-toko; do
  bd=$(curl -fsS "$GRAFANA/api/dashboards/uid/$duid" 2>/dev/null || true)
  echo "$bd" | grep -q '"folderTitle":"Sister Demo"' && ok "dashboard bab $duid terprovisioning" || salah "dashboard bab $duid tidak termuat"
done
# Query lewat proxy datasource Grafana (jalur yang dipakai panel).
gq=$(curl -fsS -G "$GRAFANA/api/datasources/proxy/uid/loki/loki/api/v1/query_range" \
  --data-urlencode 'query=sum by (level) (count_over_time({compose_project="sister-obs-uji"}[1m]))' \
  --data-urlencode "since=10m" 2>/dev/null || true)
echo "$gq" | grep -q '"level":"info"' && ok "query panel volume per level lewat Grafana berhasil" || salah "query lewat Grafana gagal: $(echo "$gq" | head -c 200)"
code=$(curl -s -o /dev/null -w '%{http_code}' "$GRAFANA/d/sister-log-per-demo/log-per-demo")
[ "$code" = 200 ] && ok "halaman dashboard HTTP 200" || salah "halaman dashboard HTTP $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$GRAFANA/api/dashboards/db" -H 'Content-Type: application/json' -d '{"dashboard":{"title":"x"}}')
[ "$code" = 401 ] || [ "$code" = 403 ] && ok "anonymous tidak bisa menulis dashboard (HTTP $code)" || salah "anonymous bisa menulis? HTTP $code"

echo
echo "== Ringkasan: $lolos lolos, $gagal gagal"
[ "$gagal" = 0 ]
