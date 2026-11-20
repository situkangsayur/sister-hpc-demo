#!/usr/bin/env bash
# Uji otomatis Bab 1 Cluster Visualizer: up --build, tunggu sehat, panggil endpoint utama,
# suntik kegagalan, periksa hasil, lalu selalu down -v (trap).
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
H="${BIND_ADDR:-127.0.0.1}"
UI="http://$H:50100"
LB="http://$H:50101"
MON="http://$H:50102"
DC=(docker compose)

lulus=0
gagal=0
ok()   { echo "  [OK]    $*"; lulus=$((lulus + 1)); }
salah() { echo "  [GAGAL] $*"; gagal=$((gagal + 1)); }
cek()  { local pesan="$1"; shift; if "$@"; then ok "$pesan"; else salah "$pesan"; fi; }

bersih() { echo "== down -v"; "${DC[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; }
trap bersih EXIT

sisa_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
if [ "$sisa_gb" -lt 8 ]; then
  echo "Sisa disk ${sisa_gb} GB < 8 GB; build dibatalkan."
  trap - EXIT
  exit 2
fi

echo "== up --build (sisa disk ${sisa_gb} GB)"
"${DC[@]}" up -d --build --wait --wait-timeout 300

# tunggu sampai kondisi terpenuhi, maksimal $1 detik
tunggu() {
  local batas=$1; shift
  for _ in $(seq 1 "$batas"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}
status_node() { curl -sf "$MON/api/status" | jq -r --arg n "$1" '.nodes[] | select(.nama==$n) | .status'; }
semua_hidup() { [ "$(curl -sf "$MON/api/status" | jq '[.nodes[] | select(.status=="hidup")] | length')" = 3 ]; }
fault() { curl -sf -X POST "$MON/api/fault/$1" -H 'content-type: application/json' -d "$2" >/dev/null; }
pulihkan() { for n in node1 node2 node3; do fault "$n" '{"mode":"normal"}' || true; done; }

echo "== layanan dasar"
cek "UI melayani halaman (HTTP 200)" tunggu 60 bash -c "curl -sf -o /dev/null '$UI/'"
cek "halaman UI berisi judul" bash -c "curl -sf '$UI/' | grep -q 'Cluster Visualizer'"
cek "ketiga node hidup menurut monitor" tunggu 30 semua_hidup
cek "API UI (/api/status lewat proxy Next.js) berisi 3 node" bash -c "[ \"\$(curl -sf '$UI/api/status' | jq '.nodes | length')\" = 3 ]"
cek "SSE /api/stream lewat UI mengirim event data" bash -c "curl -sN --max-time 3 '$UI/api/stream' | grep -q '^data: {'"
cek "nginx menjawab GET /api/sapa dengan header X-Node" bash -c "curl -sfi '$LB/api/sapa' | grep -qi '^x-node: node'"

nodes_terpakai=$(for _ in $(seq 1 30); do curl -sfi "$LB/api/sapa" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-node"{print $2}'; done | sort -u | wc -l)
cek "round robin: 30 GET tersebar ke 3 node (dapat $nodes_terpakai)" test "$nodes_terpakai" = 3
cek "POST /api/catat menulis ke Postgres (201)" bash -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -X POST '$LB/api/catat')\" = 201 ]"
cek "generator lalu lintas mencatat request" tunggu 10 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq '.kumulatif.sukses')\" -gt 5 ]"
cek "statistik DB terbaca monitor" tunggu 10 bash -c "curl -sf '$MON/api/status' | jq -e '.db.ok and .db.total >= 1' >/dev/null"

echo "== fault: node2 mati (connection refused)"
fault node2 '{"mode":"mati"}'
cek "monitor menyatakan node2 mati" tunggu 10 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq -r '.nodes[1].status')\" = mati ]"
sukses=0; dari2=0
for _ in $(seq 1 20); do
  h=$(curl -s -o /dev/null -D - -w '%{http_code}' "$LB/api/sapa" | tr -d '\r')
  echo "$h" | tail -1 | grep -q '^200$' && sukses=$((sukses + 1))
  echo "$h" | grep -qi '^x-node: node2' && dari2=$((dari2 + 1))
done
cek "20/20 GET tetap sukses lewat nginx walau node2 mati (dapat $sukses)" test "$sukses" = 20
cek "tidak ada jawaban dari node2 (dapat $dari2)" test "$dari2" = 0
fault node2 '{"mode":"normal"}'
cek "node2 hidup lagi setelah dipulihkan" tunggu 10 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq -r '.nodes[1].status')\" = hidup ]"

echo "== fault: drop response 100% di node1 (kasus c: diproses, jawaban hilang)"
sebelum=$(curl -sf "$LB/api/statistik" | jq '.per_node.node1 // 0')
fault node1 '{"mode":"normal","drop_response_pct":100}'
kode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 -X POST "http://$H:50111/api/catat" || true)
sesudah=$(curl -sf "$LB/api/statistik" | jq '.per_node.node1 // 0')
cek "klien langsung ke node1 tidak mendapat jawaban (kode $kode)" test "$kode" = 000
cek "tetapi baris node1 bertambah di DB ($sebelum -> $sesudah)" test "$sesudah" -gt "$sebelum"
fault node1 '{"mode":"normal"}'

echo "== fault: node3 hang (nginx timeout 2 s lalu retry GET ke node lain)"
fault node3 '{"mode":"hang"}'
cek "monitor tidak lagi menyatakan node3 hidup" tunggu 10 bash -c "s=\$(curl -sf '$MON/api/status' | jq -r '.nodes[2].status'); [ \"\$s\" = dicurigai ] || [ \"\$s\" = mati ]"
sukses=0
for _ in $(seq 1 6); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 6 "$LB/api/sapa")" = 200 ] && sukses=$((sukses + 1))
done
cek "6/6 GET tetap sukses walau node3 hang (dapat $sukses)" test "$sukses" = 6
fault node3 '{"mode":"normal"}'

echo "== fault: latency 400 ms di node2 -> status lambat"
fault node2 '{"mode":"normal","latency_ms":400}'
cek "monitor menyatakan node2 lambat" tunggu 10 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq -r '.nodes[1].status')\" = lambat ]"
fault node2 '{"mode":"normal"}'

echo "== fault: clock skew +5 s di node1"
fault node1 '{"mode":"normal","skew_ms":5000}'
cek "offset jam node1 terukur sekitar +5000 ms" tunggu 10 bash -c "curl -sf '$MON/api/status' | jq -e '.nodes[0].offset_ms > 4800 and .nodes[0].offset_ms < 5200' >/dev/null"
fault node1 '{"mode":"normal"}'

echo "== crash node1 (exit 1) lalu Docker restart"
curl -sf -X POST "$MON/api/crash/node1" >/dev/null || true
cek "monitor melihat node1 tidak menjawab" tunggu 10 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq -r '.nodes[0].status')\" != hidup ]"
cek "node1 hidup lagi setelah restart otomatis" tunggu 90 bash -c "[ \"\$(curl -sf '$MON/api/status' | jq -r '.nodes[0].status')\" = hidup ]"
[ "$(status_node node1)" = hidup ] || { "${DC[@]}" ps -a node1; "${DC[@]}" logs --tail 5 node1; }

echo "== Mengukur Sendiri"
cek "ukur RTT mengembalikan 5 target tanpa gagal" bash -c "curl -sf -X POST '$UI/api/ukur/rtt' | jq -e '(.hasil | length) == 5 and ([.hasil[].tcp_connect.gagal] | add) == 0' >/dev/null"
cek "uji throughput menghasilkan rps > 0" bash -c "curl -sf -X POST '$UI/api/ukur/throughput' -H 'content-type: application/json' -d '{\"durasi_s\":2,\"konkurensi\":4,\"metode\":\"baca\"}' | jq -e '.rps > 0' >/dev/null"
cek "monolith vs terdistribusi: hasil sama, jaringan lebih lambat" bash -c "curl -sf -X POST '$UI/api/ukur/bandingkan' -H 'content-type: application/json' -d '{\"n\":100}' | jq -e '.hasil[0].jumlah == .hasil[1].jumlah and .hasil[1].jumlah == .hasil[2].jumlah and .hasil[1].per_call_us > .hasil[0].per_call_us' >/dev/null"

echo "== kontrol lalu lintas dan reset"
cek "atur lalu lintas tulis 5 rps" bash -c "curl -sf -X POST '$UI/api/lalu-lintas' -H 'content-type: application/json' -d '{\"aktif\":true,\"rps\":5,\"metode\":\"tulis\"}' | jq -e '.metode == \"tulis\"' >/dev/null"
cek "reset mengosongkan data" bash -c "curl -sf -X POST '$UI/api/reset' | jq -e '.db_dikosongkan' >/dev/null"

echo "== log JSON"
cek "log node berformat JSON dengan service dan request_id" bash -c "'${DC[0]}' compose logs node2 2>/dev/null | grep -q '\"service\":\"node\".*\"request_id\":\"[0-9a-f]'"
cek "log nginx berformat JSON" bash -c "'${DC[0]}' compose logs nginx 2>/dev/null | grep -q '\"service\":\"nginx\"'"
pulihkan

echo
echo "Hasil: $lulus lulus, $gagal gagal"
[ "$gagal" -eq 0 ]
