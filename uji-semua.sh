#!/usr/bin/env bash
# Menjalankan uji.sh setiap folder bab (bab*/uji.sh) berurutan, plus shared/observability dan
# tools/perftest-desktop, mencatat lolos/gagal, dan selalu membersihkan.
#
# Pemakaian:
#   bash uji-semua.sh                 # semua yang ada
#   bash uji-semua.sh bab01 bab04     # hanya folder yang namanya diawali argumen
#   MIN_DISK_GB=8 bash uji-semua.sh   # batas sisa disk sebelum tiap uji (default 8 GB)
#
# Setiap uji.sh wajib membersihkan diri (trap down -v). Skrip ini menambah lapisan pengaman:
# sesudah tiap uji, project compose sister-babNN milik folder itu di-down -v dan build cache
# di-prune. Resource proyek lain tidak disentuh.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")" && pwd)
cd "$ROOT"
MIN_DISK_GB=${MIN_DISK_GB:-8}
LOGDIR=${LOGDIR:-$ROOT/tmp/uji-semua-$(date +%Y%m%d-%H%M%S)}
mkdir -p "$LOGDIR"

folders=()
for d in bab*/ shared/observability/ tools/perftest-desktop/; do
  d=${d%/}
  [ -f "$d/uji.sh" ] || continue
  if [ $# -gt 0 ]; then
    cocok=0
    for a in "$@"; do [[ $d == "$a"* || $(basename "$d") == "$a"* ]] && cocok=1; done
    [ $cocok = 1 ] || continue
  fi
  folders+=("$d")
done

if [ ${#folders[@]} -eq 0 ]; then
  echo "Tidak ada uji.sh yang cocok."; exit 1
fi

declare -A hasil durasi
SEKARANG=""

bersihkan_folder() {
  local d=$1
  [ -f "$d/compose.yaml" ] || return 0
  ( cd "$d" && docker compose down -v --remove-orphans >/dev/null 2>&1 ) || true
}

akhir() {
  [ -n "$SEKARANG" ] && bersihkan_folder "$SEKARANG"
  docker builder prune -f >/dev/null 2>&1 || true
  echo
  echo "================ Ringkasan uji-semua ================"
  local lolos=0 gagal=0
  for d in "${folders[@]}"; do
    local h=${hasil[$d]:-"tidak dijalankan"}
    printf "  %-45s %-18s %s\n" "$d" "$h" "${durasi[$d]:-}"
    case $h in LOLOS) lolos=$((lolos+1));; GAGAL*|DILEWATI*) gagal=$((gagal+1));; esac
  done
  echo "  lolos: $lolos, gagal/dilewati: $gagal   (log: $LOGDIR)"
  echo "  sisa disk: $(df -h / | awk 'NR==2{print $4}')"
}
trap akhir EXIT
trap 'echo; echo "Dihentikan pengguna."; exit 130' INT TERM

sisa_gb() { df -BG --output=avail / | tail -1 | tr -dc '0-9'; }

kode_akhir=0
for d in "${folders[@]}"; do
  echo
  echo "==================== $d ===================="
  if [ "$(sisa_gb)" -lt "$MIN_DISK_GB" ]; then
    echo "Sisa disk $(sisa_gb) GB < $MIN_DISK_GB GB: $d dilewati (jangan build saat disk sempit)."
    hasil[$d]="DILEWATI (disk)"; kode_akhir=1
    continue
  fi
  SEKARANG=$d
  mulai=$(date +%s)
  if [ -f "$d/.env.example" ] && [ ! -f "$d/.env" ]; then cp "$d/.env.example" "$d/.env"; fi
  log="$LOGDIR/$(echo "$d" | tr '/' '_').log"
  if bash "$d/uji.sh" 2>&1 | tee "$log"; then
    hasil[$d]="LOLOS"
  else
    hasil[$d]="GAGAL"; kode_akhir=1
  fi
  durasi[$d]="$(( $(date +%s) - mulai )) s"
  bersihkan_folder "$d"
  docker builder prune -f >/dev/null 2>&1 || true
  SEKARANG=""
done
exit $kode_akhir
