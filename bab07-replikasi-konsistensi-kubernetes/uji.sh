#!/usr/bin/env bash
# Uji otomatis Bab 7. Setiap profile dinyalakan, diperiksa, lalu dimatikan (down -v) secara
# berurutan agar mesin tidak terbebani. Pemakaian:
#   bash uji.sh                 # semua profile
#   bash uji.sh clock consensus # sebagian
set -uo pipefail
cd "$(dirname "$0")"
[ -f .env ] || cp .env.example .env
# shellcheck disable=SC1091
set -a; . ./.env; set +a
B="${BIND_ADDR:-127.0.0.1}"
UI="http://$B:50700"
ALL=(clock replication consensus search k8s)
PROFILES=("$@")
[ ${#PROFILES[@]} -eq 0 ] && PROFILES=("${ALL[@]}")
ALLP=(); for p in "${ALL[@]}"; do ALLP+=(--profile "$p"); done

LULUS=0; GAGAL=0
ok()   { echo "  LULUS  $*"; LULUS=$((LULUS + 1)); }
gagal(){ echo "  GAGAL  $*"; GAGAL=$((GAGAL + 1)); }
cleanup() { docker compose "${ALLP[@]}" down -v --remove-orphans >/dev/null 2>&1; }
trap cleanup EXIT

# cek_disk: jangan membangun bila sisa disk < 8 GB (aturan brief).
cek_disk() {
  local avail
  avail=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  if [ "$avail" -lt 8 ]; then
    echo "Sisa disk ${avail} GB < 8 GB. Batal membangun; coba lagi nanti."; exit 2
  fi
}

# tunggu <detik> <perintah...>: ulangi perintah sampai berhasil atau waktu habis.
tunggu() {
  local t=$1; shift
  for _ in $(seq 1 "$t"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
  return 1
}
get()  { curl -fsS --max-time 10 "$UI$1"; }
postj(){ curl -fsS --max-time 30 -H 'content-type: application/json' -X POST -d "${2:-{\}}" "$UI$1"; }
# jq tidak selalu ada; pakai python3 untuk membaca JSON.
js()   { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
cek()  { local nama=$1; shift; if "$@"; then ok "$nama"; else gagal "$nama"; fi; }
halaman() { [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$UI$1")" = 200 ]; }

up() {
  cek_disk
  echo "== profile $1: up --build"
  docker compose --profile "$1" up -d --build --quiet-pull 2>&1 | grep -Ei 'error|failed' || true
  tunggu 120 halaman / || { gagal "UI tidak menjawab"; return 1; }
}
down() { docker compose --profile "$1" down -v --remove-orphans >/dev/null 2>&1; echo "== profile $1: down -v"; }

uji_clock() {
  up clock || return
  tunggu 60 get /api/clock-p3/state
  cek "halaman /clock 200" halaman /clock
  for p in p1 p2 p3; do postj /api/clock-$p/reset >/dev/null; done
  postj /api/clock-p1/local >/dev/null; postj /api/clock-p2/local >/dev/null; postj /api/clock-p3/local >/dev/null
  postj "/api/clock-p1/send?to=P2&delay_ms=0" >/dev/null; postj /api/clock-p1/local >/dev/null
  postj "/api/clock-p2/send?to=P3&delay_ms=0" >/dev/null; postj /api/clock-p3/local >/dev/null
  local l2 l3 v3
  l2=$(get /api/clock-p2/state | js 'd["lamport"]')
  l3=$(get /api/clock-p3/state | js 'd["lamport"]')
  v3=$(get /api/clock-p3/state | js 'd["vector"]')
  cek "Lamport P2 = 4 (skenario buku)" [ "$l2" = 4 ]
  cek "Lamport P3 = 6 (skenario buku)" [ "$l3" = 6 ]
  cek "vector P3 = [2, 3, 3]" [ "$v3" = "[2, 3, 3]" ]
  local anom
  anom=$(get /api/clock-p2/state | js 'next(e["wall_ms"] for e in d["events"] if e["kind"]=="recv")')
  local snd
  snd=$(get /api/clock-p1/state | js 'next(e["wall_ms"] for e in d["events"] if e["kind"]=="send")')
  cek "skew: jam dinding P2 mencatat terima sebelum kirim" [ "$anom" -lt "$snd" ]
  down clock
}

uji_replication() {
  up replication || return
  tunggu 120 sh -c "curl -fsS '$UI/api/repl/status' | python3 -c 'import json,sys; s=json.load(sys.stdin)[\"status\"]; sys.exit(0 if s[\"primary_up\"] and s[\"replica_up\"] and s[\"replica_state\"]==\"streaming\" else 1)'"
  cek "halaman /replication 200" halaman /replication
  local st
  st=$(get /api/repl/status)
  cek "replica streaming dan hot standby" [ "$(echo "$st" | js 'd["status"]["replica_state"]+"/"+str(d["status"]["replica_in_recovery"])')" = "streaming/True" ]
  postj /api/repl/mode '{"mode":"async"}' >/dev/null
  postj /api/repl/delay '{"ms":3000}' >/dev/null
  cek "apply delay di replica = 3s" [ "$(get /api/repl/status | js 'd["status"]["recovery_min_apply_delay"]')" = 3s ]
  local sc
  sc=$(postj /api/repl/scenario '{"harga":12000}')
  cek "strong: Ani membaca nilai baru dari primary" [ "$(echo "$sc" | js '[r["fresh"] for r in d["reads"] if r["model"]=="strong" and r["who"]=="Ani"][0]')" = True ]
  cek "eventual: Ani membaca nilai usang dari replica (anomali read-your-writes)" [ "$(echo "$sc" | js '[r["fresh"] for r in d["reads"] if r["model"]=="eventual" and r["who"]=="Ani"][0]')" = False ]
  cek "read-your-writes: Ani dialihkan ke primary dan melihat nilai baru" [ "$(echo "$sc" | js '[r["source"]+str(r["fresh"]) for r in d["reads"] if r["model"]=="read-your-writes" and r["who"]=="Ani"][0]')" = primaryTrue ]
  cek "replica akhirnya konvergen (>= 2500 ms karena delay 3s)" [ "$(echo "$sc" | js 'd["converged_ms"]')" -ge 2500 ]
  postj /api/repl/delay '{"ms":1000}' >/dev/null
  postj /api/repl/mode '{"mode":"sync"}' >/dev/null
  tunggu 15 sh -c "curl -fsS '$UI/api/repl/status' | grep -q '\"sync_state\":\"sync\"'"
  sc=$(postj /api/repl/scenario '{"harga":15000}')
  cek "sync remote_apply: commit menunggu replica (>= 900 ms)" python3 -c "import sys; sys.exit(0 if $(echo "$sc" | js 'd["write_ms"]') >= 900 else 1)"
  cek "sync remote_apply: eventual dari replica langsung segar" [ "$(echo "$sc" | js '[r["fresh"] for r in d["reads"] if r["model"]=="eventual" and r["who"]=="Budi"][0]')" = True ]
  cek "riwayat lag terisi" [ "$(get /api/repl/status | js 'len(d["history"])>3')" = True ]
  down replication
}

leader_of() { get /api/raft-n1/status | js 'd.get("leader_id","")'; }
uji_consensus() {
  up consensus || return
  tunggu 60 sh -c "curl -fsS '$UI/api/raft-n1/status' | grep -q '\"leader_id\":\"n'"
  cek "halaman /consensus 200" halaman /consensus
  local L1
  L1=$(leader_of)
  cek "leader terpilih ($L1)" [ -n "$L1" ]
  local F
  F=$(for n in n1 n2 n3; do [ "$n" != "$L1" ] && echo $n && break; done)
  cek "SET lewat follower diteruskan ke leader" sh -c "curl -fsS -X POST -H 'content-type: application/json' -d '{\"key\":\"harga\",\"value\":\"12000\"}' '$UI/api/raft-$F/set' | grep -q '\"leader\":\"$L1\"'"
  sleep 1
  cek "entri tersalin ke ketiga node" sh -c "for n in n1 n2 n3; do curl -fsS '$UI/api/raft-'\$n'/status' | grep -q '\"harga\":\"12000\"' || exit 1; done"
  local T1
  T1=$(get /api/raft-$L1/status | js 'd["term"]')
  postj /api/raft-$L1/admin/crash >/dev/null
  tunggu 20 sh -c "curl -fsS '$UI/api/raft-$F/status' | python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d.get(\"leader_id\") not in (\"\", \"$L1\") else 1)'"
  local L2 T2
  L2=$(get /api/raft-$F/status | js 'd["leader_id"]')
  T2=$(get /api/raft-$F/status | js 'd["term"]')
  cek "leader baru $L2 setelah $L1 mati" [ -n "$L2" ] && [ "$L2" != "$L1" ]
  cek "term naik ($T1 -> $T2)" [ "$T2" -gt "$T1" ]
  cek "tulis tetap bisa dengan 2/3 node" sh -c "curl -fsS -X POST -H 'content-type: application/json' -d '{\"key\":\"stok\",\"value\":\"5\"}' '$UI/api/raft-$F/set' >/dev/null"
  postj /api/raft-$L1/admin/restart >/dev/null
  tunggu 20 sh -c "curl -fsS '$UI/api/raft-$L1/status' | grep -q '\"stok\":\"5\"'"
  cek "node lama hidup lagi sebagai follower dan mengejar log" sh -c "curl -fsS '$UI/api/raft-$L1/status' | grep -q '\"state\":\"follower\".*' && curl -fsS '$UI/api/raft-$L1/status' | grep -q '\"stok\":\"5\"'"
  down consensus
}

# status_os <status>: berhasil bila status kluster saat ini (bukan riwayat) sama dengan argumen.
status_os() { curl -fsS --max-time 10 "$UI/api/search/cluster" | python3 -c "import json,sys; sys.exit(0 if json.load(sys.stdin)['status']=='$1' else 1)"; }
uji_search() {
  up search || return
  tunggu 180 sh -c "curl -fsS '$UI/api/search/cluster' | grep -q '\"doc_count\":60'"
  tunggu 60 status_os green
  cek "halaman /search 200" halaman /search
  local c
  c=$(get /api/search/cluster)
  cek "kluster green, 2 node" [ "$(echo "$c" | js 'd["status"]+str(len(d["nodes"]))')" = green2 ]
  cek "6 shard STARTED (3 primary + 3 replica)" [ "$(echo "$c" | js 'sum(1 for s in d["shards"] if s["state"]=="STARTED")')" = 6 ]
  cek "primary dan replica shard yang sama di node berbeda" [ "$(echo "$c" | js 'all(len({s["node"] for s in d["shards"] if s["shard"]==i})==2 for i in "012")')" = True ]
  cek "pencarian mengembalikan hasil" [ "$(get '/api/search/search?q=kopi' | js 'd["total"]')" -gt 0 ]
  docker compose --profile search stop os-node2 >/dev/null 2>&1
  tunggu 90 status_os yellow
  c=$(get /api/search/cluster)
  cek "os-node2 mati: status yellow" [ "$(echo "$c" | js 'd["status"]')" = yellow ]
  cek "semua primary kini di os-node1 (replica dipromosikan)" [ "$(echo "$c" | js 'all(s["node"]=="os-node1" for s in d["shards"] if s["prirep"]=="p")')" = True ]
  cek "pencarian tetap jalan saat yellow" [ "$(get '/api/search/search?q=kopi' | js 'd["total"]')" -gt 0 ]
  docker compose --profile search start os-node2 >/dev/null 2>&1
  tunggu 120 status_os green
  cek "os-node2 kembali: status green lagi" [ "$(get /api/search/cluster | js 'd["status"]')" = green ]
  down search
}

uji_k8s() {
  up k8s || return
  tunggu 240 sh -c "curl -fsS '$UI/api/k8s/status' | python3 -c 'import json,sys; d=json.load(sys.stdin); x=d.get(\"deployment\") or {}; sys.exit(0 if x.get(\"ready\",0)>=3 else 1)'"
  cek "halaman /k8s 200" halaman /k8s
  local s
  s=$(get /api/k8s/status)
  cek "3 pod Ready dari Deployment katalog" [ "$(echo "$s" | js 'sum(1 for p in d["pods"] if p["ready"])')" = 3 ]
  tunggu 20 sh -c "curl -fsS '$UI/api/k8s/status' | grep -q '\"ok\":true'"
  cek "beban ke Service NodePort dijawab pod" [ "$(get /api/k8s/status | js 'sum(1 for h in d["hits"] if h["ok"])>0')" = True ]
  local victim
  victim=$(echo "$s" | js 'd["pods"][0]["name"]')
  postj /api/k8s/action "{\"action\":\"delete-pod\",\"pod\":\"$victim\"}" >/dev/null
  tunggu 90 sh -c "curl -fsS '$UI/api/k8s/status' | grep -q 'self-healing selesai'"
  cek "self-healing: pod pengganti Ready, waktu pulih tercatat" sh -c "curl -fsS '$UI/api/k8s/status' | grep -q 'self-healing selesai'"
  cek "pod $victim sudah diganti" sh -c "! curl -fsS '$UI/api/k8s/status' | python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if any(p[\"name\"]==\"$victim\" for p in d[\"pods\"]) else 1)'"
  postj /api/k8s/action '{"action":"scale","replicas":4}' >/dev/null
  tunggu 60 sh -c "curl -fsS '$UI/api/k8s/status' | python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d[\"deployment\"][\"ready\"]==4 else 1)'"
  cek "scaling ke 4 replica" [ "$(get /api/k8s/status | js 'd["deployment"]["ready"]')" = 4 ]
  postj /api/k8s/action '{"action":"clear"}' >/dev/null
  postj /api/k8s/action '{"action":"rollout","version":"v2"}' >/dev/null
  tunggu 120 sh -c "curl -fsS '$UI/api/k8s/status' | python3 -c 'import json,sys; d=json.load(sys.stdin); x=d[\"deployment\"]; sys.exit(0 if x[\"updated\"]==4 and x[\"ready\"]==4 and x[\"unavailable\"]==0 and all(p[\"version\"]==\"v2\" for p in d[\"pods\"] if not p[\"deleting\"]) and not any(p[\"deleting\"] for p in d[\"pods\"]) else 1)'"
  s=$(get /api/k8s/status)
  cek "rolling update: semua pod v2" [ "$(echo "$s" | js 'all(p["version"]=="v2" for p in d["pods"])')" = True ]
  cek "rolling update: ada 2 ReplicaSet (revisi lama dan baru)" [ "$(echo "$s" | js 'len(d["replicasets"])')" -ge 2 ]
  cek "rolling update: request dijawab v2" [ "$(echo "$s" | js 'any(h["version"]=="v2" for h in d["hits"])')" = True ]
  echo "  INFO   request gagal selama rolling update: $(echo "$s" | js 'sum(1 for h in d["hits"] if not h["ok"])') dari $(echo "$s" | js 'len(d["hits"])')"
  down k8s
}

for p in "${PROFILES[@]}"; do "uji_$p"; done
cleanup
echo
echo "Hasil: $LULUS lulus, $GAGAL gagal"
[ "$GAGAL" -eq 0 ]
