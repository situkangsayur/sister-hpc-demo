#!/usr/bin/env bash
# Uji otomatis Bab 4: bangun dan jalankan stack, panggil endpoint utama setiap
# protokol lewat APISIX dan lewat UI, periksa hasilnya, lalu bersihkan (down -v).
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
H=${BIND_ADDR:-127.0.0.1}
GW=http://$H:50401
UI=http://$H:50400
KEY=${APISIX_DEMO_KEY:-changeme-demo-key}

lulus=0
gagal=0
cek() { # cek "nama" perintah...
  local nama=$1; shift
  if "$@" >/dev/null 2>&1; then echo "  ok    $nama"; lulus=$((lulus + 1)); else echo "  GAGAL $nama"; gagal=$((gagal + 1)); fi
}
ada() { grep -q -- "$1" <<<"$2"; }

bersih() { docker compose down -v --remove-orphans >/dev/null 2>&1 || true; }
trap bersih EXIT

echo "== build dan start (sisa disk: $(df -h / | awk 'NR==2{print $4}'))"
docker compose up -d --build --wait --wait-timeout 240

echo "== tunggu gateway dan UI"
for i in $(seq 1 60); do
  curl -sf "$GW/healthz" >/dev/null && curl -sf "$UI/" >/dev/null && break
  sleep 2
done

echo "== REST JSON / XML / ETag / RFC 9457"
j=$(curl -s "$GW/v1/produk/42")
cek "REST JSON produk 42 = 37 byte" test "$j" = '{"id":42,"nama":"Kopi","harga":25000}'
x=$(curl -s -H 'Accept: application/xml' "$GW/v1/produk/42")
cek "REST XML (content negotiation)" ada '<produk><id>42</id><nama>Kopi</nama>' "$x"
etag=$(curl -sI "$GW/v1/produk/42" | awk -F': ' 'tolower($1)=="etag"{print $2}' | tr -d '\r')
cek "ETag + If-None-Match = 304" test "$(curl -s -o /dev/null -w '%{http_code}' -H "If-None-Match: $etag" "$GW/v1/produk/42")" = 304
p=$(curl -s "$GW/v1/produk/999")
cek "404 Problem Details" ada '"status":404' "$p"
cek "header X-Request-Id dari APISIX" ada 'X-Request-Id' "$(curl -sI "$GW/v1/produk/42")"
cek "OpenAPI dan Swagger UI" curl -sf "$GW/docs/openapi.yaml" -o /dev/null
cek "GraphiQL" curl -sf "$GW/docs/graphiql.html" -o /dev/null

echo "== SOAP 1.1 + WSDL"
cek "WSDL" ada '<definitions name="Katalog"' "$(curl -s "$GW/soap?wsdl")"
s=$(curl -s -H 'Content-Type: text/xml' -H 'SOAPAction: "urn:sister:katalog:v1#AmbilProduk"' \
  --data '<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><AmbilProduk xmlns="urn:sister:katalog:v1"><id>42</id></AmbilProduk></soap:Body></soap:Envelope>' "$GW/soap")
cek "SOAP AmbilProduk" ada '<AmbilProdukResponse' "$s"

echo "== GraphQL + N+1"
g=$(curl -s -H 'Content-Type: application/json' --data '{"query":"{ produk(id: 42) { id nama harga } }"}' "$GW/graphql")
cek "GraphQL produk" test "$g" = '{"data":{"produk":{"harga":25000,"id":42,"nama":"Kopi"}}}'
q='{"query":"{ daftarProduk { id penjual { nama } } }"}'
naif=$(curl -s -o /dev/null -D - -H 'Content-Type: application/json' --data "$q" "$GW/graphql" | awk -F': ' 'tolower($1)=="x-sql-queries"{print $2}' | tr -d '\r')
dl=$(curl -s -o /dev/null -D - -H 'Content-Type: application/json' -H 'X-DataLoader: on' --data "$q" "$GW/graphql" | awk -F': ' 'tolower($1)=="x-sql-queries"{print $2}' | tr -d '\r')
cek "N+1 naif = 51 query (dapat $naif)" test "$naif" = 51
cek "DataLoader = 2 query (dapat $dl)" test "$dl" = 2

echo "== APISIX key-auth + rate limit"
cek "tanpa apikey = 401" test "$(curl -s -o /dev/null -w '%{http_code}' "$GW/publik/v1/produk/42")" = 401
kode=""
for i in 1 2 3 4 5 6 7; do kode="$kode $(curl -s -o /dev/null -w '%{http_code}' -H "apikey: $KEY" "$GW/publik/v1/produk/42")"; done
cek "dengan apikey: 200 lalu 429 ($kode)" ada '200 200 200 200 200 429' "$kode"

echo "== SSE + WebSocket"
curl -sN --max-time 4 "$GW/v1/harga/stream?id=7" > /tmp/sister-bab04-sse.txt &
sleep 1
curl -s -X PUT -H 'Content-Type: application/json' --data '{"harga":7000}' "$GW/v1/produk/7/harga" >/dev/null
wait || true
cek "SSE menerima event harga" ada '"harga":7000' "$(cat /tmp/sister-bab04-sse.txt)"
rm -f /tmp/sister-bab04-sse.txt
ws=$(curl -s -i --max-time 2 -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
  -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' "$GW/v1/chat?nama=uji" || true)
cek "WebSocket 101 Switching Protocols" ada '101 Switching Protocols' "$ws"

echo "== UI dan API yang dipakai UI (lewat Next.js -> klien)"
for hal in / /realtime /andal /gateway; do
  cek "UI $hal = 200" test "$(curl -s -o /dev/null -w '%{http_code}' "$UI$hal")" = 200
done
b=$(curl -s -H 'Content-Type: application/json' --data '{"operasi":"ambil","id":42,"ulang":5}' "$UI/api/bandingkan")
cek "playground: 6 protokol ok" test "$(grep -o '"ok":true' <<<"$b" | wc -l)" = 6
cek "playground: gRPC payload 12 byte" ada '"protokol":"grpc".*"payload_resp":12,' "$b"
cek "playground: gRPC-Web lewat APISIX" ada '"protokol":"grpc-web","label":"gRPC-Web","format":"Protobuf + frame","transport":"HTTP/1.1","ok":true' "$b"
m=$(curl -s -X POST --data '{}' "$UI/api/grpc/mode")
cek "gRPC client streaming" ada 'Ringkasan{pesan: 4' "$m"
cek "gRPC bidi streaming" ada 'deal: Kopi' "$m"
rr=$(curl -s -X POST --data '{}' "$UI/api/gateway/roundrobin")
cek "round robin ke dua replica" bash -c "grep -q katalog-1 <<<'$rr' && grep -q katalog-2 <<<'$rr'"

echo "== timeout, retry, idempotency (server lambat 800 ms untuk 2 request, timeout 400 ms)"
andal() {
  curl -s -X POST --data '{}' "$UI/api/saldo/reset" >/dev/null
  curl -s -H 'Content-Type: application/json' --data "{\"protokol\":\"$1\",\"timeout_ms\":400,\"maks_percobaan\":4,\"backoff_ms\":50,\"jitter\":false,\"idempotency\":$2,\"lambat_ms\":800,\"lambat_kali\":2,\"gagal_persen\":0}" "$UI/api/andal"
}
a=$(andal rest false); cek "REST tanpa kunci: dipotong 3x" ada '"dipotong_kali":3' "$a"
a=$(andal rest true);  cek "REST dengan kunci: dipotong 1x, replay" bash -c "grep -q '\"dipotong_kali\":1' <<<'$a' && grep -q '\"dari_cache\":true' <<<'$a'"
a=$(andal grpc true);  cek "gRPC deadline + kunci: dipotong 1x" bash -c "grep -q 'DEADLINE_EXCEEDED' <<<'$a' && grep -q '\"dipotong_kali\":1' <<<'$a'"
k1=$(curl -s -H 'Content-Type: application/json' -H 'Idempotency-Key: uji-1' --data '{"akun":"budi","jumlah":1000}' "$GW/v1/bayar")
k2=$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'Idempotency-Key: uji-1' --data '{"akun":"budi","jumlah":2000}' "$GW/v1/bayar")
cek "kunci sama isi beda = 422" test "$k2" = 422

echo "== log JSON (slog)"
cek "log katalog berformat JSON dengan request_id" bash -c "docker compose logs katalog-1 --no-log-prefix | grep -q '\"service\":\"katalog\".*\"request_id\"'"

echo
echo "hasil: $lulus lulus, $gagal gagal"
[ "$gagal" -eq 0 ]
