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

echo "== layanan bank (APISIX /api/bank/* -> bank, database bank, stream BANK)"
bpost() { local path=$1 body=$2; shift 2; curl -s -H "apikey: ${KEY}" -H 'content-type: application/json' -X POST -d "$body" "$@" "${GW}/api/bank${path}"; }
saldo() { api "${GW}/api/bank/rekening/$1" | jq -r .saldo; }
last() { api "${GW}/api/monitor/streams/BANK/last?subject=$1"; }
check "GET /api/bank/cabang tanpa apikey ditolak 401" "[ \"\$(curl -s -o /dev/null -w %{http_code} ${GW}/api/bank/cabang)\" = 401 ]"
check "seed cabang >= 8 di beberapa kota" "api ${GW}/api/bank/cabang | jq -e 'length>=8 and ([.[].kota]|unique|length)>=6' >/dev/null"
N=$(bpost /nasabah '{"nama":"Mahasiswa Uji","nik":"3201010101010001","no_hp":"0811","email":"uji@contoh.id","kota":"Bogor","segmen":"ritel","cabang_id":3}')
NID=$(echo "$N" | jq -r .id)
check "daftar nasabah -> CIF dibuat" "echo '$N' | jq -e '.cif|startswith(\"CIF\")' >/dev/null"
# occurred_at opsional pada entitas (backfill DE): waktu lampau dipakai untuk kolom waktu bisnis dan event.
OCCN=$(date -u -d '10 days ago' +%Y-%m-%dT%H:%M:%SZ)
NB=$(bpost /nasabah "{\"nama\":\"Nasabah Backfill\",\"kota\":\"Medan\",\"segmen\":\"ritel\",\"cabang_id\":7,\"occurred_at\":\"${OCCN}\"}")
NBID=$(echo "$NB" | jq -r .id)
check "nasabah occurred_at 10 hari lalu -> created_at = updated_at = occurred_at" \
  "echo '$NB' | jq -e '(.created_at|startswith(\"${OCCN%Z}\")) and .updated_at==.created_at' >/dev/null"
check "event bank.nasabah.dibuat membawa occurred_at lampau" \
  "tunggu 5 'last bank.nasabah.dibuat | jq -e \".data.data.id==\\\"${NBID}\\\" and (.data.occurred_at|startswith(\\\"${OCCN%Z}\\\"))\"'"
FUT=$(date -u -d '1 hour' +%Y-%m-%dT%H:%M:%SZ)
check "nasabah occurred_at 1 jam ke depan ditolak 400" \
  "[ \"\$(curl -s -o /dev/null -w %{http_code} -H 'apikey: ${KEY}' -H 'content-type: application/json' -X POST -d '{\"nama\":\"Masa Depan\",\"occurred_at\":\"${FUT}\"}' ${GW}/api/bank/nasabah)\" = 400 ]"
check "rekening occurred_at lampau -> dibuka_at lampau, setoran awal ikut tanggal itu" \
  "bpost /rekening '{\"nasabah_id\":\"${NBID}\",\"jenis\":\"tabungan_wadiah\",\"setoran_awal\":50000,\"occurred_at\":\"${OCCN}\"}' | jq -e '.dibuka_at|startswith(\"${OCCN%Z}\")' >/dev/null && tunggu 5 'last bank.transaksi.dibuat | jq -e \".data.data.nominal==50000 and (.data.occurred_at|startswith(\\\"${OCCN%Z}\\\"))\"'"
check "pengajuan akad occurred_at lampau -> diajukan_at lampau" \
  "bpost /akad '{\"nasabah_id\":\"${NBID}\",\"kode_akad\":\"IJR\",\"pokok\":100000,\"margin\":5000,\"tenor_bulan\":2,\"occurred_at\":\"${OCCN}\"}' | jq -e '.akad.diajukan_at|startswith(\"${OCCN%Z}\")' >/dev/null"
K=$(bpost /rekening "{\"nasabah_id\":\"${NID}\",\"jenis\":\"tabungan_wadiah\",\"setoran_awal\":500000}")
REK=$(echo "$K" | jq -r .no_rekening)
check "buka rekening ${REK} dengan setoran awal 500.000" "[ \"\$(saldo ${REK})\" = 500000 ]"
AIS=$(api "${GW}/api/bank/rekening" | jq -r '[.[]|select(.nama=="Aisyah Rahmawati")][0].no_rekening')
S_AIS=$(saldo "$AIS")
T=$(bpost /transfer "{\"dari\":\"${REK}\",\"ke\":\"${AIS}\",\"nominal\":100000,\"kanal\":\"mobile\"}" -H "Idempotency-Key: uji-trf-$$")
check "transfer 100.000 -> 2 baris (keluar+masuk, transfer_id sama)" \
  "echo '$T' | jq -e '(.transaksi|length)==2 and .transaksi[0].transfer_id==.transaksi[1].transfer_id and .transaksi[0].jenis==\"transfer_keluar\"' >/dev/null"
check "saldo asal 400.000 dan tujuan bertambah 100.000" "[ \"\$(saldo ${REK})\" = 400000 ] && [ \"\$(saldo ${AIS})\" = $((S_AIS + 100000)) ]"
check "transfer ulang dengan Idempotency-Key sama -> replayed, saldo tetap" \
  "bpost /transfer '{\"dari\":\"${REK}\",\"ke\":\"${AIS}\",\"nominal\":100000,\"kanal\":\"mobile\"}' -H 'Idempotency-Key: uji-trf-$$' | jq -e '.replayed==true' >/dev/null && [ \"\$(saldo ${REK})\" = 400000 ]"
check "transfer melebihi saldo -> 409" \
  "[ \"\$(curl -s -o /dev/null -w %{http_code} -H 'apikey: ${KEY}' -H 'content-type: application/json' -X POST -d '{\"dari\":\"${REK}\",\"ke\":\"${AIS}\",\"nominal\":99999999,\"kanal\":\"web\"}' ${GW}/api/bank/transfer)\" = 409 ]"
OCC=$(date -u -d '3 days ago' +%Y-%m-%dT%H:%M:%SZ)
check "setor offline: occurred_at mundur 3 hari disimpan apa adanya" \
  "bpost /transaksi '{\"jenis\":\"setor\",\"no_rekening\":\"${REK}\",\"nominal\":25000,\"kanal\":\"atm\",\"cabang_id\":7,\"occurred_at\":\"${OCC}\"}' | jq -e '.transaksi.occurred_at|startswith(\"${OCC%Z}\")' >/dev/null"
check "ubah profil (kota/segmen) -> event bank.nasabah.diperbarui" \
  "curl -s -H 'apikey: ${KEY}' -H 'content-type: application/json' -X PATCH -d '{\"kota\":\"Depok\",\"segmen\":\"umkm\"}' ${GW}/api/bank/nasabah/${NID} | jq -e '.kota==\"Depok\"' >/dev/null && tunggu 5 'last bank.nasabah.diperbarui | jq -e \".data.data.kota==\\\"Depok\\\"\"'"
A=$(bpost /akad "{\"nasabah_id\":\"${NID}\",\"kode_akad\":\"MRB\",\"pokok\":300000,\"margin\":30000,\"tenor_bulan\":3}")
AID=$(echo "$A" | jq -r .akad.id)
bpost "/akad/${AID}/setujui" '{}' >/dev/null
check "akad MRB disetujui lalu dicairkan -> rekening +300.000" \
  "bpost /akad/${AID}/cairkan '{}' | jq -e '.akad.status==\"cair\" and .transaksi.nominal==300000' >/dev/null && [ \"\$(saldo ${REK})\" = 725000 ]"
check "angsuran akad -> transaksi jenis angsuran 110.000" "bpost /akad/${AID}/angsuran '{\"kanal\":\"mobile\"}' | jq -e '.transaksi.jenis==\"angsuran\" and .akad.terbayar==110000' >/dev/null"
check "kode akad tidak valid (XYZ) tetap tersimpan apa adanya" \
  "bpost /akad '{\"nasabah_id\":\"${NID}\",\"kode_akad\":\"XYZ\",\"pokok\":100000,\"margin\":0,\"tenor_bulan\":1}' | jq -e '.akad.kode_akad==\"XYZ\" and .kode_valid==false' >/dev/null"

echo "== belanja toko dibayar dari rekening bank"
S0=$(saldo "$REK")
R=$(post /api/orders "{\"items\":[{\"product_id\":\"P013\",\"qty\":1}],\"no_rekening\":\"${REK}\"}" -H "Idempotency-Key: uji-rek-$$")
OIDB=$(echo "$R" | jq -r .order.id)
check "checkout dibayar dari rekening -> saga ${OIDB} CONFIRMED" "tunggu 30 '[ \"\$(saga ${OIDB})\" = CONFIRMED ]'"
check "saldo ${REK} berkurang 98.000 (harga P013)" "[ \"\$(saldo ${REK})\" = $((S0 - 98000)) ]"
check "transaksi bayar_belanja kanal ecommerce dengan ref_order_id ${OIDB}" \
  "api ${GW}/api/bank/rekening/${REK}/mutasi | jq -e '[.[]|select(.jenis==\"bayar_belanja\" and .kanal==\"ecommerce\" and .ref_order_id==\"${OIDB}\")]|length==1' >/dev/null"
CIT=$(api "${GW}/api/bank/rekening" | jq -r '[.[]|select(.nama=="Citra Lestari")][0].no_rekening')
S_CIT=$(saldo "$CIT")
STOK9=$(api "${GW}/api/monitor/stock" | jq '.[]|select(.product_id=="P009").available')
OIDK=$(post /api/orders "{\"items\":[{\"product_id\":\"P009\",\"qty\":1}],\"no_rekening\":\"${CIT}\"}" -H "Idempotency-Key: uji-kurang-$$" | jq -r .order.id)
check "saldo kurang -> payment gagal -> saga ${OIDK} dikompensasi" "tunggu 30 '[ \"\$(saga ${OIDK})\" = \"CANCELLED (dikompensasi)\" ]'"
check "saldo Citra tetap dan stok P009 kembali" \
  "[ \"\$(saldo ${CIT})\" = ${S_CIT} ] && tunggu 10 '[ \"\$(api ${GW}/api/monitor/stock | jq \".[]|select(.product_id==\\\"P009\\\").available\")\" = ${STOK9} ]'"
check "alasan batal menyebut saldo tidak cukup" "api ${GW}/api/orders/${OIDK} | jq -e '.order.reason|test(\"saldo tidak cukup\")' >/dev/null"

echo "== checkout dengan occurred_at lampau (backfill DE)"
lastshop() { api "${GW}/api/monitor/streams/SHOP/last?subject=$1"; }
OCCO=$(date -u -d '5 days ago' +%Y-%m-%dT%H:%M:%SZ)
R=$(post /api/orders "{\"items\":[{\"product_id\":\"P003\",\"qty\":1}],\"no_rekening\":\"${REK}\",\"occurred_at\":\"${OCCO}\"}" -H "Idempotency-Key: uji-occ-$$")
OIDO=$(echo "$R" | jq -r .order.id)
check "checkout occurred_at 5 hari lalu -> respons created_at lampau" "echo '$R' | jq -e '.order.created_at|startswith(\"${OCCO%Z}\")' >/dev/null"
check "event shop.order.created membawa occurred_at lampau (field tambahan)" \
  "tunggu 5 'lastshop shop.order.created | jq -e \".data.order_id==\\\"${OIDO}\\\" and (.data.occurred_at|startswith(\\\"${OCCO%Z}\\\"))\"'"
check "saga ${OIDO} CONFIRMED, orders.created_at tetap lampau" \
  "tunggu 30 '[ \"\$(saga ${OIDO})\" = CONFIRMED ]' && api ${GW}/api/orders/${OIDO} | jq -e '.order.created_at|startswith(\"${OCCO%Z}\")' >/dev/null"
check "bayar_belanja ${OIDO} tercatat pada tanggal checkout" \
  "api ${GW}/api/bank/rekening/${REK}/mutasi | jq -e '[.[]|select(.ref_order_id==\"${OIDO}\" and (.occurred_at|startswith(\"${OCCO%Z}\")))]|length==1' >/dev/null"
check "checkout occurred_at 1 jam ke depan ditolak 400" \
  "[ \"\$(curl -s -o /dev/null -w %{http_code} -H 'apikey: ${KEY}' -H 'content-type: application/json' -X POST -d '{\"items\":[{\"product_id\":\"P003\",\"qty\":1}],\"occurred_at\":\"${FUT}\"}' ${GW}/api/orders)\" = 400 ]"

echo "== event BANK (kontrak data DE)"
check "stream BANK ada dan berisi event" "api ${GW}/api/monitor/streams | jq -e '.[]|select(.name==\"BANK\")|.messages>=10' >/dev/null"
L=$(last bank.transaksi.dibuat)
check "payload {event_id, event_type, occurred_at, data} dan Nats-Msg-Id = event_id" \
  "echo '$L' | jq -e '.data|has(\"event_id\") and has(\"event_type\") and has(\"occurred_at\") and has(\"data\")' >/dev/null && echo '$L' | jq -e '.headers[\"Nats-Msg-Id\"][0]==.data.event_id' >/dev/null"
check "event bayar_belanja ${OIDB} tampil di timeline monitor" \
  "api ${GW}/api/monitor/events?since=0 | jq -e '[.events[]|select(.subject==\"bank.transaksi.dibuat\" and .order_id==\"${OIDB}\")]|length>=1' >/dev/null"
for subj in bank.cabang.dibuat bank.nasabah.dibuat bank.rekening.dibuka bank.akad.diajukan bank.akad.disetujui bank.akad.dicairkan bank.akad.diangsur; do
  check "subject ${subj} terbit" "last ${subj} | jq -e '.data.event_type==\"${subj}\"' >/dev/null"
done

echo "== PostgreSQL siap untuk Debezium (CDC)"
pgq() { docker compose exec -T postgres psql -U sister -d "$1" -tAc "$2"; }
check "SHOW wal_level = logical" "[ \"\$(pgq postgres 'SHOW wal_level')\" = logical ]"
check "max_replication_slots dan max_wal_senders >= 4" "[ \"\$(pgq postgres 'SHOW max_replication_slots')\" -ge 4 ] && [ \"\$(pgq postgres 'SHOW max_wal_senders')\" -ge 4 ]"
check "role ${DEBEZIUM_USER:-debezium} punya hak REPLICATION" "[ \"\$(pgq postgres \"SELECT rolreplication FROM pg_roles WHERE rolname='${DEBEZIUM_USER:-debezium}'\")\" = t ]"
check "publication dbz_bank berisi 5 tabel bank" "[ \"\$(pgq bank \"SELECT count(*) FROM pg_publication_tables WHERE pubname='dbz_bank'\")\" = 5 ]"
check "REPLICA IDENTITY FULL (relreplident='f') pada nasabah, rekening, akad_pembiayaan" \
  "[ \"\$(pgq bank \"SELECT string_agg(relname::text || '=' || relreplident::text, ',' ORDER BY relname) FROM pg_class WHERE relkind='r' AND relname IN ('nasabah','rekening','akad_pembiayaan')\")\" = 'akad_pembiayaan=f,nasabah=f,rekening=f' ]"
check "REPLICA IDENTITY FULL pada orders" "[ \"\$(pgq orders \"SELECT relreplident FROM pg_class WHERE relkind='r' AND relname='orders'\")\" = f ]"
# Bukti "before" terisi: slot test_decoding sementara merekam UPDATE nasabah. Dengan REPLICA IDENTITY
# FULL baris lama lengkap ada di WAL (old-key), sumber field "before" pada envelope Debezium.
before_nasabah() {
  local occ chg
  occ=$(date -u -d '2 days ago' +%Y-%m-%dT%H:%M:%SZ)
  pgq bank "SELECT 1 FROM pg_create_logical_replication_slot('uji_before', 'test_decoding')" >/dev/null || return 1
  curl -s -H "apikey: ${KEY}" -H 'content-type: application/json' -X PATCH \
    -d "{\"kota\":\"Binjai\",\"occurred_at\":\"${occ}\"}" "${GW}/api/bank/nasabah/${NBID}" >/dev/null
  chg=$(pgq bank "SELECT data FROM pg_logical_slot_get_changes('uji_before', NULL, NULL) WHERE data LIKE 'table public.nasabah: UPDATE:%'")
  pgq bank "SELECT pg_drop_replication_slot('uji_before')" >/dev/null
  echo "$chg" | grep -q "old-key:.*kota\[text\]:'Medan'.*new-tuple:.*kota\[text\]:'Binjai'" || return 1
  api "${GW}/api/bank/nasabah/${NBID}" | jq -e --arg o "${occ%Z}" '.nasabah.updated_at|startswith($o)' >/dev/null || return 1
  tunggu 5 "last bank.nasabah.diperbarui | jq -e '.data.data.kota==\"Binjai\" and (.data.occurred_at|startswith(\"${occ%Z}\"))'"
}
check "update nasabah: WAL membawa baris lama (before kota Medan), updated_at/event = occurred_at PATCH" before_nasabah
check "publication dbz_orders berisi tabel orders" "[ \"\$(pgq orders \"SELECT count(*) FROM pg_publication_tables WHERE pubname='dbz_orders' AND tablename='orders'\")\" = 1 ]"
check "role debezium dapat SELECT transaksi dan membuat slot logical (pgoutput)" \
  "docker compose exec -T -e PGPASSWORD=\"${DEBEZIUM_PASSWORD:-changeme-debezium}\" postgres psql -h 127.0.0.1 -U ${DEBEZIUM_USER:-debezium} -d bank -tAc 'SELECT count(*) FROM transaksi' >/dev/null && docker compose exec -T -e PGPASSWORD=\"${DEBEZIUM_PASSWORD:-changeme-debezium}\" postgres psql 'host=127.0.0.1 dbname=bank replication=database user=${DEBEZIUM_USER:-debezium}' -c 'CREATE_REPLICATION_SLOT uji_tmp TEMPORARY LOGICAL pgoutput' | grep -q uji_tmp"

echo "== UI Next.js"
check "UI melayani halaman (HTTP 200)" "[ \"\$(curl -s -o /dev/null -w %{http_code} ${UI}/)\" = 200 ]"
check "UI berisi judul demo" "curl -s ${UI}/ | grep -q 'Toko Daring Microservices'"
check "API UI /api/gw/monitor/health: 13 komponen up (termasuk bank)" \
  "curl -s ${UI}/api/gw/monitor/health | jq -e '[.[]|select(.status==\"up\")]|length==13' >/dev/null"
check "API UI /api/gw/bank/rekening mengembalikan rekening" "curl -s ${UI}/api/gw/bank/rekening | jq -e 'length>=3' >/dev/null"
check "API UI /api/gw/bank/transaksi (mutasi live) berisi transaksi" "curl -s ${UI}/api/gw/bank/transaksi | jq -e 'length>=1' >/dev/null"
check "API UI /api/gw/monitor/saga berisi order" "curl -s ${UI}/api/gw/monitor/saga | jq -e 'length>=5' >/dev/null"
check "API UI /api/gw/catalog/search mengembalikan produk" "curl -s '${UI}/api/gw/catalog/search?q=batik' | jq -e '.hits|length>=1' >/dev/null"
sleep 11 # jendela rate limit sebelumnya habis
check "rate limit APISIX: burst 30 -> ada HTTP 429" "curl -s -X POST -d '{\"n\":30}' ${UI}/api/burst | jq -e '.by_status[\"429\"]>=1 and .by_status[\"200\"]<=20' >/dev/null"

echo
echo "Hasil: ${LULUS} lulus, ${GAGAL} gagal"
[ "$GAGAL" -eq 0 ]
