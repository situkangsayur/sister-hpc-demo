#!/usr/bin/env bash
# Uji otomatis demo Bab 5: build, nyalakan, uji endpoint utama dan skenario kegagalan, lalu down -v.
# Pakai: bash uji.sh   (butuh docker compose, curl, jq)
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
HOST="${BIND_ADDR:-127.0.0.1}"
GW="http://${HOST}:50501"
UI="http://${HOST}:50500"
KEY="${APISIX_KEY:-changeme-storefront-key}"

LULUS=0
GAGAL=0
ok()   { echo "  [LULUS] $*"; LULUS=$((LULUS + 1)); }
fail() { echo "  [GAGAL] $*"; GAGAL=$((GAGAL + 1)); }
check() { if eval "$2"; then ok "$1"; else fail "$1"; fi; }

bersih() {
  echo "== membersihkan (docker compose down -v)"
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap bersih EXIT
trap "exit 130" INT TERM

sisa=$(df --output=avail -BG / | tail -1 | tr -dc '0-9')
if [ "${sisa:-0}" -lt 8 ]; then
  echo "Sisa disk ${sisa} GB (< 8 GB). Tunggu disk lega sebelum build." >&2
  trap - EXIT
  exit 2
fi

echo "== build dan start (sisa disk ${sisa} GB)"
docker compose up -d --build --wait --wait-timeout 420

api()  { curl -s -H "apikey: ${KEY}" "$@"; }
post() { local path=$1 body=$2; shift 2; curl -s -H "apikey: ${KEY}" -H 'content-type: application/json' -X POST -d "$body" "$@" "${GW}${path}"; }
tunggu() { # tunggu <detik> <perintah-kondisi>
  local n=$1; shift
  for _ in $(seq 1 "$n"); do if eval "$*" >/dev/null 2>&1; then return 0; fi; sleep 1; done
  return 1
}
saga() { api "${GW}/api/monitor/saga" | jq -r --arg id "$1" '.[] | select(.order_id==$id) | .outcome'; }
order() { # order <idempotency-key> <card> <product> -> JSON respons
  post /api/orders "{\"items\":[{\"product_id\":\"$3\",\"qty\":1}],\"card\":\"$2\"}" -H "Idempotency-Key: $1"
}

echo "== gateway dan pencarian (APISIX -> catalog -> OpenSearch 2 node)"
check "APISIX menjawab /gw/ping" "tunggu 60 '[ \"\$(curl -s -o /dev/null -w %{http_code} ${GW}/gw/ping)\" = 200 ]'"
check "catalog memakai OpenSearch (index siap)" "tunggu 120 '[ \"\$(curl -s ${GW}/api/catalog/search?q=kopi | jq -r .source)\" = opensearch ]'"
check "pencarian fuzzy 'raspbery' menemukan P015" "tunggu 20 'curl -s \"${GW}/api/catalog/search?q=raspbery\" | jq -e \".hits[0].id==\\\"P015\\\"\"'"
check "cluster OpenSearch 2 node, index products 4 shard aktif" \
  "tunggu 60 'api ${GW}/api/monitor/opensearch | jq -e \".health.number_of_nodes==2 and ([.shards[]|select(.state==\\\"STARTED\\\")]|length)==4\"'"
check "primary dan replica tersebar di 2 node" \
  "api ${GW}/api/monitor/opensearch | jq -e '[.shards[].node]|unique|length==2' >/dev/null"
check "request id dari APISIX ada di header respons" "curl -si ${GW}/api/catalog/products/P001 | grep -qi '^x-request-id:'"

echo "== key-auth"
check "POST /api/orders tanpa apikey ditolak 401" \
  "[ \"\$(curl -s -o /dev/null -w %{http_code} -X POST -d '{}' ${GW}/api/orders)\" = 401 ]"

echo "== saga sukses (T1..T4 + notifikasi)"
R=$(order "uji-ok-$$" ok P001)
OID=$(echo "$R" | jq -r '.order.id')
RID=$(echo "$R" | jq -r '.order.request_id')
check "checkout 201 dengan order id" "[ \"${OID}\" != null ] && [ -n \"${OID}\" ]"
check "saga ${OID} CONFIRMED" "tunggu 30 '[ \"\$(saga ${OID})\" = CONFIRMED ]'"
check "notifikasi terkirim untuk ${OID}" "tunggu 15 'api ${GW}/api/monitor/notifications | jq -e \".[]|select(.order_id==\\\"${OID}\\\" and .kind==\\\"confirmed\\\")\"'"
check "Idempotency-Key sama -> pesanan lama (replayed)" "order uji-ok-$$ ok P001 | jq -e '.replayed==true and .order.id==\"${OID}\"' >/dev/null"
check "request_id ${RID:0:8}.. muncul di log JSON order dan inventory" \
  "docker compose logs order-1 order-2 inventory 2>/dev/null | grep \"${RID}\" | grep -q '\"service\":\"inventory\"'"

echo "== saga gagal bayar -> kompensasi C2, C1"
STOK0=$(api "${GW}/api/monitor/stock" | jq '.[]|select(.product_id=="P002").available')
R=$(order "uji-decl-$$" declined P002)
OID2=$(echo "$R" | jq -r '.order.id')
check "saga ${OID2} CANCELLED (dikompensasi)" "tunggu 30 '[ \"\$(saga ${OID2})\" = \"CANCELLED (dikompensasi)\" ]'"
check "stok P002 kembali ke ${STOK0} setelah C2" \
  "tunggu 10 '[ \"\$(api ${GW}/api/monitor/stock | jq \".[]|select(.product_id==\\\"P002\\\").available\")\" = ${STOK0} ]'"
post /api/monitor/faults/payment '{"fail_rate":1}' >/dev/null
OID3=$(order "uji-fail-$$" ok P003 | jq -r '.order.id')
check "fault payment 100% -> saga ${OID3} dikompensasi" "tunggu 30 '[ \"\$(saga ${OID3})\" = \"CANCELLED (dikompensasi)\" ]'"
post /api/monitor/faults/payment '{"fail_rate":0}' >/dev/null

echo "== nginx load balancer 2 replica"
SERVED=$(for _ in 1 2 3 4 5 6; do curl -s -D - -o /dev/null -H "apikey: ${KEY}" "${GW}/api/orders" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-served-by"{print $2}'; done | sort -u | tr '\n' ' ')
check "request dibagi ke order-1 dan order-2 (${SERVED})" "[ \"${SERVED}\" = 'order-1 order-2 ' ]"
post /api/monitor/faults/replica/order-1 '{"down":true}' >/dev/null
CODES=$(for _ in 1 2 3 4 5 6; do curl -s -o /dev/null -w '%{http_code} ' -H "apikey: ${KEY}" "${GW}/api/orders"; done)
check "order-1 dimatikan: semua request tetap 200 (${CODES})" "[ \"${CODES}\" = '200 200 200 200 200 200 ' ]"
OID4=$(order "uji-1rep-$$" ok P004 | jq -r '.order.served_by')
check "checkout dilayani order-2 saat order-1 mati (${OID4})" "[ \"${OID4}\" = order-2 ]"
check "monitor melaporkan order-1 simulated-down" "api ${GW}/api/monitor/health | jq -e '.[]|select(.name==\"order-1\").status==\"simulated-down\"' >/dev/null"
post /api/monitor/faults/replica/order-1 '{"down":false}' >/dev/null

echo "== timeout, retry, circuit breaker (inventory lambat 800 ms > timeout 300 ms)"
post /api/monitor/faults/inventory '{"delay_ms":800}' >/dev/null
for i in 1 2 3 4 5 6; do order "uji-cb-$$-$i" ok P005 >/dev/null; done
check "breaker inventory OPEN di minimal satu replica" \
  "api ${GW}/api/monitor/breakers | jq -e '[.[].breakers[]?|select(.name==\"inventory\" and .state==\"open\")]|length>=1' >/dev/null"
R=$(order "uji-cb-$$-x" ok P005)
check "checkout tetap 201 dengan precheck fallback" "echo '$R' | jq -e '.order.id and (.precheck|startswith(\"fallback\"))' >/dev/null"
post /api/monitor/faults/inventory '{"delay_ms":0}' >/dev/null

echo "== at-least-once: crash sebelum ack -> redelivery -> duplikat terdeteksi"
post /api/monitor/faults/inventory '{"crash_before_ack":true}' >/dev/null
OID5=$(order "uji-crash-$$" ok P006 | jq -r '.order.id')
check "event crash tercatat untuk ${OID5}" \
  "tunggu 15 'api ${GW}/api/monitor/events?since=0 | jq -e \"[.events[]|select(.order_id==\\\"${OID5}\\\" and .action==\\\"crash\\\")]|length==1\"'"
check "redelivery dideteksi sebagai duplicate (consumer idempotent)" \
  "tunggu 20 'api ${GW}/api/monitor/events?since=0 | jq -e \"[.events[]|select(.order_id==\\\"${OID5}\\\" and .action==\\\"duplicate\\\")]|length>=1\"'"
check "saga ${OID5} tetap CONFIRMED sekali" "tunggu 20 '[ \"\$(saga ${OID5})\" = CONFIRMED ]'"
post /api/monitor/faults/inventory '{"crash_before_ack":false}' >/dev/null

echo "== lab delivery semantics (core NATS vs JetStream)"
post /api/monitor/lab/reset '{}' >/dev/null
post /api/monitor/lab/publish '{"mode":"core","n":10}' >/dev/null
check "core online: queue group membagi 10 pesan, audit-log menerima 10" \
  "tunggu 5 'api ${GW}/api/monitor/lab/stats | jq -e \".receiver.received[\\\"worker-a\\\"]+.receiver.received[\\\"worker-b\\\"]==10 and .receiver.received[\\\"audit-log\\\"]==10\"'"
post /api/monitor/lab/reset '{}' >/dev/null
post /api/monitor/lab/offline '{"offline":true}' >/dev/null
post /api/monitor/lab/publish '{"mode":"core","n":10}' >/dev/null
post /api/monitor/lab/publish '{"mode":"js","n":10}' >/dev/null
check "penerima offline: JetStream menahan 10 pesan" "api ${GW}/api/monitor/lab/stats | jq -e '.js_pending==10' >/dev/null"
post /api/monitor/lab/offline '{"offline":false}' >/dev/null
check "penerima online: 10 pesan JetStream diterima, 10 pesan core hilang" \
  "tunggu 10 'api ${GW}/api/monitor/lab/stats | jq -e \".receiver.received[\\\"js-worker\\\"]==10 and ((.receiver.received[\\\"worker-a\\\"]//0)+(.receiver.received[\\\"worker-b\\\"]//0))==0\"'"

echo "== UI Next.js"
check "UI melayani halaman (HTTP 200)" "[ \"\$(curl -s -o /dev/null -w %{http_code} ${UI}/)\" = 200 ]"
check "UI berisi judul demo" "curl -s ${UI}/ | grep -q 'Toko Daring Microservices'"
check "API UI /api/gw/monitor/health: 12 komponen up" \
  "curl -s ${UI}/api/gw/monitor/health | jq -e '[.[]|select(.status==\"up\")]|length==12' >/dev/null"
check "API UI /api/gw/monitor/saga berisi order" "curl -s ${UI}/api/gw/monitor/saga | jq -e 'length>=5' >/dev/null"
check "API UI /api/gw/catalog/search mengembalikan produk" "curl -s '${UI}/api/gw/catalog/search?q=batik' | jq -e '.hits|length>=1' >/dev/null"
sleep 11 # jendela rate limit sebelumnya habis
check "rate limit APISIX: burst 30 -> ada HTTP 429" "curl -s -X POST -d '{\"n\":30}' ${UI}/api/burst | jq -e '.by_status[\"429\"]>=1 and .by_status[\"200\"]<=20' >/dev/null"

echo
echo "Hasil: ${LULUS} lulus, ${GAGAL} gagal"
[ "$GAGAL" -eq 0 ]
