#!/usr/bin/env bash
# Uji otomatis demo CI/CD Jenkins + Argo CD:
#   up --build -> tunggu init -> build #1 ter-deploy -> push kode lewat UI (webhook -> Jenkins ->
#   registry -> gitops -> Argo CD) -> versi baru jalan -> push kode rusak (CI menolak) ->
#   drift (selfHeal) -> rollback lewat Git -> down -v (trap, termasuk volume k3s dan registry).
set -euo pipefail
cd "$(dirname "$0")"

[ -f .env ] || bash buat-env.sh
set -a; . ./.env; set +a
H="${BIND_ADDR:-127.0.0.1}"
UI="http://$H:51000"
DC=(docker compose)
MULAI=$(date +%s)

lulus=0
gagal=0
ok()    { echo "  [OK]    $*"; lulus=$((lulus + 1)); }
salah() { echo "  [GAGAL] $*"; gagal=$((gagal + 1)); }
cek()   { local pesan="$1"; shift; if "$@"; then ok "$pesan"; else salah "$pesan"; fi; }
t()     { echo "  ($(($(date +%s) - MULAI)) s) $*"; }

bersih() {
  echo "== down -v (hapus container dan volume: k3s, registry, Jenkins, Gitea, BuildKit)"
  "${DC[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap bersih EXIT

sisa_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
if [ "$sisa_gb" -lt 8 ]; then
  echo "Sisa disk ${sisa_gb} GB < 8 GB; build dibatalkan."
  trap - EXIT
  exit 2
fi

echo "== up --build (sisa disk ${sisa_gb} GB)"
"${DC[@]}" up -d --build

# tunggu <batas detik> <perintah...>
tunggu() {
  local batas=$1; shift
  local akhir=$(($(date +%s) + batas))
  while [ "$(date +%s)" -lt "$akhir" ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  return 1
}
status() { curl -sf "$UI/api/status"; }
q() { status | jq -e "$1" >/dev/null; }
post() { curl -sf -X POST "$UI/api/$1" -H 'content-type: application/json' -d "$2"; }
export UI; export -f status q post

echo "== init (Gitea, Jenkins, Argo CD); pertama kali 3-6 menit"
kode=$(timeout 900 docker wait "$("${DC[@]}" ps -aq init)" || echo 99)
cek "init selesai dengan kode 0 (dapat $kode)" test "$kode" = 0
[ "$kode" = 0 ] || { "${DC[@]}" logs --tail 20 init; exit 1; }
t "init selesai"

echo "== layanan dan UI"
cek "UI melayani halaman (HTTP 200)" tunggu 60 bash -c "curl -sf -o /dev/null '$UI/'"
cek "halaman UI berisi judul" bash -c "curl -sf '$UI/' | grep -q 'CI/CD Pipeline Viewer'"
cek "API UI /api/status berisi data Jenkins dan Gitea" tunggu 60 q '.jenkins.ok and .app.ok and .gitops.ok'
cek "Gitea melayani repo app (HTTP 200)" bash -c "curl -sf -o /dev/null 'http://$H:51001/$GITEA_ADMIN_USER/app'"
cek "Jenkins menolak akses anonim (403)" bash -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' 'http://$H:51003/job/app/api/json')\" = 403 ]"
cek "Argo CD UI melayani halaman (HTTP 200)" bash -c "curl -sf -o /dev/null 'http://$H:51006/'"

echo "== putaran 1: build #1 (dipicu init) sampai ter-deploy"
cek "build #1 SUCCESS dengan 4 tahap SUCCESS" tunggu 420 q '.jenkins.builds[] | select(.nomor==1) | .hasil=="SUCCESS" and ([.tahap[]?.status]==["SUCCESS","SUCCESS","SUCCESS","SUCCESS"])'
TAG1=$(status | jq -r '.jenkins.builds[] | select(.nomor==1) | .tag')
cek "tag $TAG1 ada di registry" q ".registry | index(\"$TAG1\")"
cek "repo gitops berisi newTag $TAG1" tunggu 30 q ".gitops.tag==\"$TAG1\""
cek "Argo CD Synced + Healthy, semua jawaban dari $TAG1" tunggu 240 q ".argo.sync==\"Synced\" and .argo.health==\"Healthy\" and (.berjalan.versi|keys)==[\"$TAG1\"]"
t "versi pertama berjalan"

echo "== putaran 2: ubah kode dan push lewat UI (webhook Gitea -> Jenkins)"
PESAN="uji otomatis $(date +%H%M%S)"
cek "POST /api/push membuat commit" bash -c "post push '{\"pesan\":\"$PESAN\"}' | jq -e .commit"
cek "webhook memicu build #2 dan build SUCCESS" tunggu 300 q '.jenkins.builds[] | select(.nomor==2) | .hasil=="SUCCESS"'
TAG2=$(status | jq -r '.jenkins.builds[] | select(.nomor==2) | .tag')
cek "Argo CD men-sync $TAG2; semua jawaban versi baru dengan pesan baru" tunggu 180 \
  q ".argo.sync==\"Synced\" and .argo.health==\"Healthy\" and (.berjalan.versi|keys)==[\"$TAG2\"] and (.berjalan.respons|all(.pesan==\"$PESAN\"))"
cek "langsung dari NodePort: versi $TAG2" bash -c "curl -sf 'http://$H:51005/' | jq -e '.versi==\"$TAG2\"'"
cek "riwayat deploy Argo CD mencatat $TAG1 dan $TAG2" q "[.argo.riwayat[].tag] | index(\"$TAG1\") and index(\"$TAG2\")"
t "versi kedua berjalan"

echo "== putaran 3: push kode rusak (unit test harus gagal, cluster tidak berubah)"
cek "POST /api/push gagal=true membuat commit" bash -c "post push '{\"gagal\":true}' | jq -e .commit"
cek "build #3 FAILURE di tahap Unit test, tahap sesudahnya SKIPPED" tunggu 240 \
  q '.jenkins.builds[] | select(.nomor==3) | .hasil=="FAILURE" and ([.tahap[].status]==["SUCCESS","FAILED","SKIPPED","SKIPPED"])'
cek "gitops tetap $TAG2 dan aplikasi tetap $TAG2" q ".gitops.tag==\"$TAG2\" and (.berjalan.versi|keys)==[\"$TAG2\"]"

echo "== drift: scale manual ke 4 replica, selfHeal mengembalikan ke 2"
cek "POST /api/drift diterima" bash -c "post drift '{}' >/dev/null"
cek "jumlah pod sempat > 2" tunggu 30 q '(.argo.pods|length) > 2'
cek "selfHeal: kembali 2 pod, Synced + Healthy" tunggu 120 q '(.argo.pods|length)==2 and .argo.sync=="Synced" and .argo.health=="Healthy"'

echo "== rollback lewat Git ke $TAG1"
cek "POST /api/rollback membuat commit gitops" bash -c "post rollback '{\"tag\":\"$TAG1\"}' | jq -e .commit"
cek "gitops newTag kembali $TAG1" tunggu 20 q ".gitops.tag==\"$TAG1\""
cek "Argo CD men-sync rollback; semua jawaban $TAG1" tunggu 180 q ".argo.sync==\"Synced\" and .argo.health==\"Healthy\" and (.berjalan.versi|keys)==[\"$TAG1\"]"
cek "log event mencatat rollback" q '[.events[].pesan] | any(test("rollback app ke"))'
cek "rollback ke tag yang tidak ada ditolak (400)" bash -c "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -X POST '$UI/api/rollback' -d '{\"tag\":\"b99-0000000\"}')\" = 400 ]"
t "rollback selesai"

b=$(command -v chromium || command -v chromium-browser || command -v google-chrome || true)
if [ -n "$b" ]; then
  png="${TMPDIR:-/tmp}/sister-cicd-ui.png"
  timeout 90 "$b" --headless --disable-gpu --no-sandbox --hide-scrollbars --window-size=1400,2000 \
    --user-data-dir="$(mktemp -d)" \
    --virtual-time-budget=6000 --screenshot="$png" "$UI/" >/dev/null 2>&1 && echo "  tangkapan layar UI: $png"
fi

echo "== ukuran"
docker image ls --format '{{.Repository}}:{{.Tag}} {{.Size}}' | grep -E '^sister-cicd' || true
docker system df -v 2>/dev/null | grep -E 'sister-cicd_' | awk '{print "  volume", $1, $3}' || true

echo
echo "Hasil: $lulus lulus, $gagal gagal ($(($(date +%s) - MULAI)) s)"
[ "$gagal" = 0 ]
