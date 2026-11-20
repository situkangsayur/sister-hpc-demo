#!/usr/bin/env bash
# Uji otomatis Bab 6 (Concurrency Lab): build, jalankan, panggil endpoint utama lewat UI
# (termasuk stream SSE yang diteruskan proxy Next.js), periksa hasil, lalu down -v.
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
BIND="${BIND_ADDR:-127.0.0.1}"
UI="http://${BIND}:50600"
LAB="http://${BIND}:50601"

# UJI_NO_BUILD=1 memakai image yang sudah ada (tanpa build), mis. saat disk server menipis.
BUILD_FLAG="--build"
if [ "${UJI_NO_BUILD:-0}" = 1 ]; then
  BUILD_FLAG=""
else
  avail_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  if [ "${avail_gb}" -lt 8 ]; then
    echo "Sisa disk ${avail_gb} GB < 8 GB: batal membangun (tunggu, atau UJI_NO_BUILD=1 bila image sudah ada)." >&2
    exit 1
  fi
fi

cleanup() { docker compose down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== up ${BUILD_FLAG}"
docker compose up -d ${BUILD_FLAG} --wait

fail=0
ok()   { echo "  OK   $*"; }
bad()  { echo "  GAGAL $*"; fail=1; }
warn() { echo "  WARN $*"; }
py()   { python3 -c "$1"; }

echo "== UI dan health"
code=$(curl -s -o /tmp/bab06-ui.html -w '%{http_code}' "$UI/")
if [ "$code" = 200 ] && grep -q "Concurrency Lab" /tmp/bab06-ui.html; then ok "UI 200 dan memuat judul"; else bad "UI HTTP $code"; fi
rm -f /tmp/bab06-ui.html
curl -sf "$UI/api/health" | grep -q '"status":"ok"' && ok "health lewat proxy UI" || bad "health lewat proxy UI"
curl -sf "$LAB/api/health" | grep -q '"status":"ok"' && ok "health langsung ke lab" || bad "health lab"

echo "== 1. race condition"
r=$(curl -sf -XPOST "$UI/api/race" -H 'content-type: application/json' -d '{"goroutines":1000,"iters":1000,"trials":5}')
echo "$r" | py '
import json,sys
d=json.load(sys.stdin)
for m in d["results"]:
    print("    %-8s hasil=%s median=%.1f ms" % (m["mode"], [t["got"] for t in m["trials"]], m["median_ms"]))
safe=[m for m in d["results"] if m["mode"]!="unsafe"]
sys.exit(0 if all(m["all_correct"] for m in safe) else 1)' && ok "mode mutex/atomic/channel/actor selalu tepat" || bad "mode aman salah"
echo "$r" | py 'import json,sys; d=json.load(sys.stdin); u=[m for m in d["results"] if m["mode"]=="unsafe"][0]; sys.exit(0 if not u["all_correct"] else 1)' \
  && ok "mode unsafe kehilangan penambahan (race terlihat)" || warn "mode unsafe kebetulan tepat kali ini"

d=$(curl -sf -XPOST "$UI/api/race/detector" -H 'content-type: application/json' -d '{"mode":"unsafe"}')
echo "$d" | py 'import json,sys; d=json.load(sys.stdin); print("    race_reports=%d exit=%d" % (d["race_reports"], d["exit_code"])); sys.exit(0 if d["race_reports"]>0 else 1)' \
  && ok "race detector melaporkan DATA RACE pada unsafe" || bad "race detector tidak melapor"
d=$(curl -sf -XPOST "$UI/api/race/detector" -H 'content-type: application/json' -d '{"mode":"atomic"}')
echo "$d" | py 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["race_reports"]==0 and d["exit_code"]==0 else 1)' \
  && ok "race detector bersih pada atomic" || bad "race detector atomic tidak bersih"

echo "== 2. deadlock (SSE lewat proxy UI)"
done_ev() { curl -sN --max-time 16 "$UI/api/deadlock/stream?$1" | awk '/^event: done/{getline; sub(/^data: /,""); print; exit}'; }
n=$(done_ev "strategy=naive&threads=2&hold_ms=200")
echo "    naive:   $n"
echo "$n" | py 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["status"]=="deadlock" else 1)' && ok "strategi naif berakhir deadlock (siklus terdeteksi)" || bad "naif tidak deadlock"
o=$(done_ev "strategy=ordered&threads=3&hold_ms=100")
echo "    ordered: $o"
echo "$o" | py 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["status"]=="selesai" and d["transfers"]==d["expected_transfers"] and d["total"]==d["expected_total"] else 1)' \
  && ok "urutan lock global: semua transfer selesai, total saldo tetap" || bad "ordered gagal"
t=$(done_ev "strategy=trylock&threads=2&hold_ms=100")
echo "$t" | py 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["status"]=="selesai" else 1)' && ok "tryLock + jitter selesai" || bad "trylock gagal: $t"

echo "== 3. worker pool"
p=$(curl -sf -XPOST "$UI/api/pool/sweep" -H 'content-type: application/json' -d '{"kind":"io","jobs":100,"job_ms":10,"workers":[1,64],"gomaxprocs":[1]}')
echo "$p" | py '
import json,sys
pts=json.load(sys.stdin)["series"][0]["points"]
print("    io: 1 worker=%.0f/s, 64 worker=%.0f/s" % (pts[0]["throughput"], pts[1]["throughput"]))
sys.exit(0 if pts[1]["throughput"] > 10*pts[0]["throughput"] else 1)' && ok "I/O-bound naik >10x dengan 64 worker walau GOMAXPROCS=1" || bad "pool io"
p=$(curl -sf -XPOST "$UI/api/pool/sweep" -H 'content-type: application/json' -d '{"kind":"cpu","jobs":100,"job_ms":2,"workers":[1,16],"gomaxprocs":[1]}')
echo "$p" | py '
import json,sys
pts=json.load(sys.stdin)["series"][0]["points"]
print("    cpu GOMAXPROCS=1: 1 worker=%.0f/s, 16 worker=%.0f/s" % (pts[0]["throughput"], pts[1]["throughput"]))
sys.exit(0 if pts[1]["throughput"] < 2*pts[0]["throughput"] else 1)' && ok "CPU-bound tidak naik berarti saat GOMAXPROCS=1" || bad "pool cpu"

echo "== 4. pipeline CSP dan backpressure"
s=$(curl -sN --max-time 4 "$UI/api/pipeline/stream?workers=3&buffer=4&produce_ms=5&work_ms=10&sink_ms=100" | grep '^data: {"t_ms' | tail -1 | sed 's/^data: //' || true)
echo "$s" | py '
import json,sys
d=json.load(sys.stdin)
print("    produced=%d sunk=%d jobs=%d/%d producer_blocked=%.0f%% sink_rate=%.1f/s" % (d["produced"], d["sunk"], d["jobs_len"], d["jobs_cap"], d["producer_blocked"]*100, d["sink_rate"]))
sys.exit(0 if d["sunk"]>0 and d["jobs_len"]==d["jobs_cap"] and d["producer_blocked"]>0.5 else 1)' \
  && ok "sink lambat: buffer penuh dan generator tertahan (backpressure)" || bad "pipeline: $s"

echo "== 5. goroutine vs thread OS"
g=$(curl -sf -XPOST "$UI/api/spawn" -H 'content-type: application/json' -d '{"kind":"goroutine","n":50000}')
echo "$g" | py 'import json,sys; d=json.load(sys.stdin); print("    goroutine: stack/unit=%.0f B rss/unit=%.0f B" % (d["stack_bytes_per_unit"], d["rss_bytes_per_unit"])); sys.exit(0 if 0 < d["stack_bytes_per_unit"] < 16384 else 1)' \
  && ok "50.000 goroutine, stack per goroutine < 16 KB" || bad "spawn goroutine"
th=$(curl -sf -XPOST "$UI/api/spawn" -H 'content-type: application/json' -d '{"kind":"thread","n":500}')
echo "$th" | py 'import json,sys; d=json.load(sys.stdin); print("    thread: %d -> %d thread OS, rss/unit=%.0f B" % (d["threads_before"], d["threads_after"], d["rss_bytes_per_unit"])); sys.exit(0 if d["threads_after"] >= 500 else 1)' \
  && ok "LockOSThread memaksa >= 500 thread OS" || bad "spawn thread"
curl -sf -XPOST "$UI/api/spawn/release" | grep -q released && ok "release" || bad "release"
curl -sf -XPOST "$UI/api/gc/churn" -H 'content-type: application/json' -d '{"seconds":1,"mb_per_s":200,"live_mb":16}' \
  | py 'import json,sys; d=json.load(sys.stdin); print("    gc_count=%d pause_max=%.0f us" % (d["gc_count"], d["pause_max_us"])); sys.exit(0 if d["gc_count"]>0 else 1)' \
  && ok "beban GC memicu siklus GC" || bad "gc churn"
{ curl -sN --max-time 2 "$UI/api/metrics/stream" || true; } | grep -q '^event: metrics' && ok "stream metrik runtime (SSE)" || bad "metrics stream"

echo "== 6. actor dan event loop"
a=$(curl -sN --max-time 3 "$UI/api/actor/stream?actors=4&rate=200&process_ms=1" | grep '^data: {"t_ms' | tail -1 | sed 's/^data: //' || true)
echo "$a" | py 'import json,sys; d=json.load(sys.stdin); print("    sent=%d total=%d in_flight=%d" % (d["sent"], d["total"], d["in_flight"])); sys.exit(0 if d["total"]>0 and d["sent"]==d["total"]+d["in_flight"] else 1)' \
  && ok "actor: terkirim = saldo + isi mailbox" || bad "actor: $a"
tmp=$(mktemp)
curl -sN --max-time 4 "$UI/api/eventloop/stream?seconds=10" > "$tmp" &
cpid=$!
sleep 1
sid=$(grep -o '"session":"[a-f0-9]*"' "$tmp" | head -1 | cut -d'"' -f4)
curl -sf -XPOST "$UI/api/session/${sid}?block=400" >/dev/null || true
wait "$cpid" || true
lag=$(grep '^data: {"t_ms' "$tmp" | sed 's/^data: //' | py 'import json,sys; print(max(json.loads(l)["lag_max_ms"] for l in sys.stdin))')
rm -f "$tmp"
echo "    lag maks sesudah callback berat 400 ms: ${lag} ms"
py "import sys; sys.exit(0 if float('${lag}') > 200 else 1)" && ok "callback berat membekukan event loop" || bad "event loop lag"

echo "== log JSON"
lablog=$(docker compose logs lab --no-log-prefix 2>/dev/null || true)
echo "$lablog" | grep -q '"service":"lab".*"request_id"' && ok "log lab berformat JSON (slog)" || bad "log lab"

if [ "$fail" = 0 ]; then echo "SEMUA UJI LOLOS"; else echo "ADA UJI GAGAL"; fi
exit "$fail"
