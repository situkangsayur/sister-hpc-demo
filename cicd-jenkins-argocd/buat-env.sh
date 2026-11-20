#!/usr/bin/env bash
# Membuat .env dari .env.example dengan password acak (hex) untuk setiap nilai "changeme-...".
# Pakai: bash buat-env.sh [BIND_ADDR]
set -euo pipefail
cd "$(dirname "$0")"
if [ -f .env ]; then echo ".env sudah ada; hapus dulu bila ingin membuat ulang."; exit 0; fi
acak() { head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
while IFS= read -r baris; do
  case "$baris" in
    *=changeme*) echo "${baris%%=*}=$(acak)" ;;
    BIND_ADDR=*) echo "BIND_ADDR=${1:-127.0.0.1}" ;;
    *) echo "$baris" ;;
  esac
done < .env.example > .env
chmod 600 .env
echo ".env dibuat (password acak). Lihat isinya untuk login ke Gitea, Jenkins, dan Argo CD."
