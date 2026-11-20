#!/usr/bin/env bash
# Uji otomatis perftest-desktop tanpa layar:
#   1) gofmt, go vet, unit test mesin (engine, presets) dengan -race,
#   2) build CLI (CGO_ENABLED=0) dan server contoh demo-target,
#   3) jalankan CLI terhadap demo-target untuk keempat protokol (HTTP, GraphQL, gRPC, WebSocket),
#      cek exit code, jumlah request, ekspor JSON/CSV, dan klasifikasi error,
#   4) bila binary desktop sudah dibangun (build/bin/perftest-desktop) dan Xvfb tersedia,
#      jalankan GUI headless dengan --autostart dan simpan screenshot (opsional).
# Selalu mematikan demo-target dan Xvfb di akhir. Tidak memakai Docker.
set -euo pipefail
cd "$(dirname "$0")"
export GOTOOLCHAIN=local
TMP=$(mktemp -d)
PIDS=()
bersih() {
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  rm -rf "$TMP"
}
trap bersih EXIT

lolos=0; gagal=0
ok()   { echo "  [LOLOS] $*"; lolos=$((lolos+1)); }
salah(){ echo "  [GAGAL] $*"; gagal=$((gagal+1)); }

echo "== gofmt / go vet / unit test"
[ -z "$(gofmt -l engine presets internal cmd ./*.go)" ] && ok "gofmt bersih" || salah "gofmt: $(gofmt -l engine presets internal cmd ./*.go)"
go vet ./... && ok "go vet bersih" || salah "go vet"
go test -race -count=1 ./engine/ ./presets/ && ok "unit test engine + presets (-race)" || salah "unit test"

echo "== build CLI dan demo-target"
CGO_ENABLED=0 go build -o "$TMP/perftest" ./cmd/perftest && ok "build CLI statik" || salah "build CLI"
CGO_ENABLED=0 go build -o "$TMP/demo-target" ./cmd/demo-target && ok "build demo-target" || salah "build demo-target"

HTTP=127.0.0.1:50098; GRPC=127.0.0.1:50099
if ss -ltn 2>/dev/null | grep -qE ":(50098|50099)\b"; then
  salah "port 50098/50099 sedang dipakai; hentikan demo-target lain dulu"
else
  "$TMP/demo-target" -http "$HTTP" -grpc "$GRPC" -error-rate 0.05 >"$TMP/target.log" 2>&1 &
  PIDS+=($!)
  for i in $(seq 1 50); do curl -fs "http://$HTTP/api/cepat" >/dev/null 2>&1 && break; sleep 0.1; done

  echo "== CLI terhadap demo-target"
  for f in demo-graphql demo-grpc demo-ws; do
    if out=$("$TMP/perftest" run --config "examples/$f.yaml" --quiet --max-error-rate 0 --json "$TMP/$f.json" 2>&1); then
      grep -q "request: 500  ok: 500  error: 0" <<<"$out" && ok "$f: 500 request sukses" || salah "$f: $out"
    else
      salah "$f gagal: $out"
    fi
  done
  out=$("$TMP/perftest" run --config examples/demo-lambat.yaml --duration 3s --rps 200 --quiet --csv "$TMP/l.csv" --json "$TMP/l.json" 2>&1) || true
  grep -q "http_5xx=" <<<"$out" && ok "HTTP /api/lambat: error 503 terklasifikasi http_5xx" || salah "klasifikasi 5xx: $out"
  tot=$(grep -oE 'request: [0-9]+' <<<"$out" | grep -oE '[0-9]+' || echo 0)
  [ "${tot:-0}" -ge 450 ] && [ "${tot:-0}" -le 700 ] && ok "target RPS 200 x 3 s menghasilkan $tot request" || salah "jumlah request RPS: $tot"
  [ "$(grep -c '' "$TMP/l.csv")" -ge 4 ] && tail -1 "$TMP/l.csv" | grep -q '^total,' && ok "ekspor CSV (timeline + baris total)" || salah "CSV"
  grep -q '"p99Ms"' "$TMP/l.json" && grep -q '"histogram"' "$TMP/l.json" && ok "ekspor JSON (latency + histogram)" || salah "JSON"
  "$TMP/perftest" grpc-list --target "$GRPC" | grep -q "grpc.health.v1.Health/Check" && ok "grpc-list lewat server reflection" || salah "grpc-list"
  if "$TMP/perftest" run --config examples/demo-lambat.yaml --duration 2s --quiet --max-error-rate 0 >/dev/null 2>&1; then
    salah "--max-error-rate 0 seharusnya gagal saat ada 503"
  else
    ok "--max-error-rate memberi exit code 1 saat ada error"
  fi
  "$TMP/perftest" presets | grep -q "^4\.6 .*grpc" && ok "preset bab tersedia (perftest presets)" || salah "presets"

  # GUI headless (opsional)
  if [ -x build/bin/perftest-desktop ] && command -v Xvfb >/dev/null && command -v import >/dev/null; then
    echo "== GUI headless (Xvfb)"
    D=:$((50 + RANDOM % 40))
    Xvfb "$D" -screen 0 1440x920x24 >/dev/null 2>&1 &
    PIDS+=($!)
    sleep 1
    DISPLAY=$D GDK_BACKEND=x11 WEBKIT_DISABLE_COMPOSITING_MODE=1 LIBGL_ALWAYS_SOFTWARE=1 \
      build/bin/perftest-desktop --config examples/demo-lambat.yaml --autostart >"$TMP/gui.log" 2>&1 &
    PIDS+=($!)
    sleep 20
    if DISPLAY=$D import -window root "$TMP/gui.png" 2>/dev/null && [ -s "$TMP/gui.png" ]; then
      mkdir -p build/bin && cp "$TMP/gui.png" build/bin/uji-gui.png
      ok "GUI berjalan di Xvfb; screenshot build/bin/uji-gui.png"
    else
      salah "screenshot GUI"
    fi
  else
    echo "  (lewati GUI: binary desktop belum dibangun atau Xvfb/ImageMagick tidak ada)"
  fi
fi

echo
echo "== Ringkasan: $lolos lolos, $gagal gagal"
[ "$gagal" = 0 ]
