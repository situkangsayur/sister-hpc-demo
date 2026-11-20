#!/usr/bin/env bash
# Uji otomatis Bab 3: up --build, tunggu sehat, panggil endpoint utama, periksa hasil, lalu down -v.
set -euo pipefail
cd "$(dirname "$0")"
[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
B="${BIND_ADDR:-127.0.0.1}"
UI="http://$B:50300"
P="$UI/api/probe"

bersih() { docker compose down -v --remove-orphans >/dev/null 2>&1 || true; }
trap bersih EXIT

gagal() { echo "GAGAL: $*" >&2; docker compose ps >&2; docker compose logs --tail=30 >&2 || true; exit 1; }
cek() { # cek <nama> <ekspresi-jq> <json>
  if echo "$3" | jq -e "$2" >/dev/null; then echo "  ok  $1"; else echo "$3" | head -c 1500 >&2; echo >&2; gagal "$1"; fi
}

echo "== disk"; df -h / | tail -1
echo "== build dan start"
docker compose up -d --build --wait --wait-timeout 240 || gagal "compose up"

echo "== UI melayani halaman"
for p in / /tls /sniff /depan; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$UI$p")
  [ "$code" = 200 ] || gagal "UI $p HTTP $code"
  echo "  ok  UI $p 200"
done

echo "== netem"
J=$(curl -sf "$P/netem")
cek "netem terpasang (delay ${NETEM_DELAY_MS:-25} ms)" ".delay_ms == ${NETEM_DELAY_MS:-25} and (.err // \"\") == \"\"" "$J"

echo "== waterfall h1 vs h2 vs h3"
J=$(curl -sf "$P/compare?n=30&runs=3")
cek "tiga protokol diukur" '.results | length == 3' "$J"
cek "tidak ada request error" '[.results[].sample.requests[] | select(.err != null and .err != "")] | length == 0' "$J"
cek "h1: ALPN http/1.1, 2..6 koneksi" '.results[] | select(.proto=="h1") | .alpn == "http/1.1" and .conns >= 2 and .conns <= 6' "$J"
cek "h2: ALPN h2, 1 koneksi" '.results[] | select(.proto=="h2") | .alpn == "h2" and .conns == 1' "$J"
cek "h3: ALPN h3, 1 koneksi QUIC" '.results[] | select(.proto=="h3") | .alpn == "h3" and .conns == 1' "$J"
cek "31 request per pemuatan" '[.results[].sample.requests | length] | all(. == 31)' "$J"
cek "h1 lebih lambat dari h2 (RTT buatan)" '(.results[] | select(.proto=="h1") | .median_ms) > (.results[] | select(.proto=="h2") | .median_ms)' "$J"
echo "$J" | jq -r '.results[] | "      \(.proto): median \(.median_ms|floor) ms, \(.conns) koneksi, \(.tls_version)"'

echo "== TLS"
J=$(curl -sf "$P/tls?target=origin&max=1.3")
cek "origin TLS 1.3, ALPN h2, rantai 2 dikirim, 3 terverifikasi" '.version=="TLS 1.3" and .alpn=="h2" and (.peer_chain|length)==2 and (.verified_chain|length)==3' "$J"
cek "ditolak trust store sistem dan nama host lain" '.checks[0].ok and (.checks[1].ok|not) and (.checks[2].ok|not)' "$J"
J=$(curl -sf "$P/tls?target=nginx&max=1.2")
cek "nginx TLS 1.2 dengan cipher ECDHE" '.version=="TLS 1.2" and (.cipher|test("ECDHE"))' "$J"
J=$(curl -sf "$P/mtls")
cek "mTLS: sah diterima, tanpa cert dan penyusup ditolak" '.attempts[0].ok and (.attempts[1].ok|not) and (.attempts[2].ok|not)' "$J"

echo "== HTTP polos vs HTTPS"
J=$(curl -sf "$P/sniff?mode=http")
cek "password terlihat di HTTP polos" '.secret_visible == true and .status == 200' "$J"
J=$(curl -sf "$P/sniff?mode=https")
cek "password tidak terlihat di HTTPS, SNI terbaca" '.secret_visible == false and .status == 200 and .sni == "origin-a" and ([.records[].kind] | index("ApplicationData") != null)' "$J"

echo "== nginx: cache, ETag, gzip, HSTS"
J=$(curl -sf "$P/cache?path=/api/katalog&gzip=1")
cek "GET pertama MISS dan memanggil origin" '.status==200 and .headers["X-Cache-Status"]=="MISS" and .origin_delta==1' "$J"
ETAG=$(echo "$J" | jq -r '.headers.ETag')
J=$(curl -sf "$P/cache?path=/api/katalog&gzip=1")
cek "GET kedua HIT tanpa origin, gzip" '.headers["X-Cache-Status"]=="HIT" and .origin_delta==0 and .headers["Content-Encoding"]=="gzip"' "$J"
GZ=$(echo "$J" | jq '.wire_bytes')
J=$(curl -sf "$P/cache?path=/api/katalog&gzip=0")
RAW=$(echo "$J" | jq '.wire_bytes')
[ "$GZ" -lt "$RAW" ] || gagal "gzip tidak memperkecil ($GZ vs $RAW)"; echo "  ok  gzip $GZ B < polos $RAW B"
J=$(curl -sf -G "$P/cache" --data-urlencode "path=/api/katalog" --data-urlencode "inm=$ETAG" --data-urlencode "gzip=1")
cek "If-None-Match → 304 tanpa body" '.status==304 and .wire_bytes==0' "$J"
sleep 6
J=$(curl -sf "$P/cache?path=/api/katalog&gzip=1")
cek "sesudah max-age: REVALIDATED (origin 304)" '.headers["X-Cache-Status"]=="REVALIDATED"' "$J"
J=$(curl -sf "$P/hsts")
cek "http 301 ke https, HSTS di https" '.http.status==301 and (.http.headers.Location|startswith("https://")) and (.https.headers["Strict-Transport-Security"]|test("max-age"))' "$J"

echo "== load balancer"
J=$(curl -sf "$P/lb?n=10")
cek "kedua origin mendapat request" '.counts["origin-a"] > 0 and .counts["origin-b"] > 0' "$J"
curl -sf -X POST "$P/lb/sakit?instance=origin-b&on=1" >/dev/null
J=$(curl -sf "$P/lb?n=10")
cek "origin-b sakit: semua 200 dari origin-a" '.counts["origin-a"] == 10 and ([.hits[].status] | all(. == 200))' "$J"

echo "== log JSON"
docker compose logs probe --no-log-prefix 2>/dev/null | grep -m1 '"service":"probe"' | jq -e '.time and .level and .msg' >/dev/null || gagal "log probe bukan JSON"
echo "  ok  log probe JSON satu baris"

echo "SEMUA UJI BAB 3 LOLOS"
