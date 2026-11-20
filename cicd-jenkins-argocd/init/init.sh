#!/usr/bin/env bash
# Init sekali jalan (idempotent, aman diulang setiap `docker compose up`):
#   1. Gitea : user bot, repo app + gitops (isi awal dari /seed), hak tulis bot, webhook ke Jenkins
#   2. Jenkins: picu build #1 bila job belum pernah dibangun
#   3. k3s   : DNS "gitea" untuk pod, pasang Argo CD (versi dipin + cek sha256), set password admin,
#              lalu buat Application yang mengawasi repo gitops
set -euo pipefail

ARGOCD_VERSI=v3.4.13
ARGOCD_SHA256=2904fe432572425803e6519d59ded69f79ce0b1da38c4952c5989b83051bb445
ARGOCD_URL="https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSI}/manifests/install.yaml"

G=http://gitea:3000
J=http://jenkins:8080
A=http://k3s:30081
OWNER="$GITEA_ADMIN_USER"
BOT="$GITEA_BOT_USER"
K=(kubectl --kubeconfig /kube/kubeconfig.yaml --server https://k3s:6443)

# Log satu baris JSON agar rapi di Loki/Grafana.
log() { jq -cn --arg m "$*" '{time: (now | todate), level: "INFO", service: "init", msg: $m}'; }
gagal() { jq -cn --arg m "$*" '{time: (now | todate), level: "ERROR", service: "init", msg: $m}'; exit 1; }

# tunggu <deskripsi> <batas detik> <perintah...>
tunggu() {
  local desk=$1 batas=$2; shift 2
  log "menunggu $desk (maks ${batas} s)"
  for _ in $(seq 1 "$batas"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  gagal "$desk tidak siap dalam ${batas} s"
}

gapi() { curl -sS -f -u "$GITEA_ADMIN_USER:$GITEA_ADMIN_PASSWORD" -H 'content-type: application/json' "$@"; }
uri() { jq -rn --arg v "$1" '$v | @uri'; }

# ------------------------------------------------------------------ 1. Gitea
tunggu "Gitea" 180 curl -sf "$G/api/healthz"
tunggu "user admin Gitea ($OWNER)" 120 gapi "$G/api/v1/user"

bot_json=$(jq -cn --arg u "$BOT" --arg p "$GITEA_BOT_PASSWORD" \
  '{username: $u, login_name: $u, source_id: 0, email: ($u + "@demo.local"), password: $p, must_change_password: false}')
if gapi "$G/api/v1/users/$BOT" >/dev/null 2>&1; then
  gapi -X PATCH "$G/api/v1/admin/users/$BOT" -d "$bot_json" >/dev/null
  log "user $BOT sudah ada; password disamakan dengan .env"
else
  gapi -X POST "$G/api/v1/admin/users" -d "$bot_json" >/dev/null
  log "user $BOT dibuat"
fi

isi_repo() { # isi_repo <nama>: push isi /seed/<nama> sebagai commit pertama
  local r=$1 d
  d=$(mktemp -d)
  cp -r "/seed/$r/." "$d"
  git -C "$d" init -q -b main
  git -C "$d" add -A
  git -C "$d" -c user.name="$OWNER" -c user.email="$OWNER@demo.local" commit -q -m "awal: isi repo $r"
  git -C "$d" push -q "http://$(uri "$OWNER"):$(uri "$GITEA_ADMIN_PASSWORD")@gitea:3000/$OWNER/$r.git" main
  rm -rf "$d"
  log "repo $OWNER/$r diisi dari seed"
}

for r in app gitops; do
  if ! gapi "$G/api/v1/repos/$OWNER/$r" >/dev/null 2>&1; then
    gapi -X POST "$G/api/v1/user/repos" -d "$(jq -cn --arg n "$r" \
      '{name: $n, private: false, default_branch: "main", description: ("Demo CI/CD: repo " + $n)}')" >/dev/null
    log "repo $OWNER/$r dibuat"
  fi
  if [ "$(gapi "$G/api/v1/repos/$OWNER/$r" | jq -r .empty)" = "true" ]; then isi_repo "$r"; fi
done

gapi -X PUT "$G/api/v1/repos/$OWNER/gitops/collaborators/$BOT" -d '{"permission":"write"}' >/dev/null
log "$BOT mendapat hak tulis di $OWNER/gitops"

HOOK_URL="$J/git/notifyCommit?url=$G/$OWNER/app.git"
if ! gapi "$G/api/v1/repos/$OWNER/app/hooks" | jq -e --arg u "$HOOK_URL" '.[] | select(.config.url == $u)' >/dev/null; then
  gapi -X POST "$G/api/v1/repos/$OWNER/app/hooks" -d "$(jq -cn --arg u "$HOOK_URL" \
    '{type: "gitea", active: true, events: ["push"], config: {url: $u, content_type: "json", http_method: "post"}}')" >/dev/null
  log "webhook push repo app -> Jenkins notifyCommit dibuat"
fi

# ------------------------------------------------------------------ 2. Jenkins
JA=(-u "$JENKINS_ADMIN_USER:$JENKINS_ADMIN_PASSWORD")
tunggu "job Jenkins 'app' (dibuat JCasC + Job DSL)" 300 curl -sf "${JA[@]}" "$J/job/app/api/json"
info=$(curl -sfg "${JA[@]}" "$J/job/app/api/json?tree=inQueue,builds[number]")
if [ "$(jq '.builds | length' <<<"$info")" = 0 ] && [ "$(jq -r .inQueue <<<"$info")" != "true" ]; then
  jar=$(mktemp)
  crumb=$(curl -sf -c "$jar" "${JA[@]}" "$J/crumbIssuer/api/json")
  curl -sf -b "$jar" "${JA[@]}" -X POST -H "$(jq -r '.crumbRequestField + ": " + .crumb' <<<"$crumb")" \
    "$J/job/app/build" -o /dev/null
  rm -f "$jar"
  log "build #1 Jenkins dipicu (berikutnya dipicu webhook push)"
fi

# ------------------------------------------------------------------ 3. k3s + Argo CD
tunggu "k3s API" 300 "${K[@]}" get --raw /readyz

# Pod di k3s tidak bisa memakai DNS Docker; nama "gitea" dipetakan ke IP container lewat
# ConfigMap coredns-custom (dibaca ulang setiap init, sebab IP container bisa berubah).
GITEA_IP=$(getent hosts gitea | awk '{print $1; exit}')
[ -n "$GITEA_IP" ] || gagal "IP gitea tidak ditemukan"
lama=$("${K[@]}" -n kube-system get configmap coredns-custom -o jsonpath='{.data.gitea\.server}' 2>/dev/null || true)
baru=$(printf 'gitea:53 {\n    hosts {\n        %s gitea\n    }\n}\n' "$GITEA_IP")
if [ "$lama" != "$baru" ]; then
  "${K[@]}" -n kube-system create configmap coredns-custom --from-literal=gitea.server="$baru" \
    --dry-run=client -o yaml | "${K[@]}" apply -f - >/dev/null
  tunggu "deployment coredns" 180 "${K[@]}" -n kube-system get deploy coredns
  "${K[@]}" -n kube-system rollout restart deploy/coredns >/dev/null
  log "DNS pod: gitea -> $GITEA_IP"
fi

W=$(mktemp -d)
cp /argocd/* "$W/"
curl -sfL --retry 3 -o "$W/install.yaml" "$ARGOCD_URL"
echo "$ARGOCD_SHA256  $W/install.yaml" | sha256sum -c - >/dev/null || gagal "sha256 install.yaml Argo CD tidak cocok"
"${K[@]}" create namespace argocd --dry-run=client -o yaml | "${K[@]}" apply -f - >/dev/null
"${K[@]}" apply --server-side --force-conflicts -k "$W" >/dev/null
# Password admin di-set sebelum argocd-server siap. Login yang gagal berulang (mis. dari pantau)
# membuat Argo CD mengunci akun admin sementara, jadi jangan menunggu server dulu.
lama=$("${K[@]}" -n argocd get secret argocd-secret -o jsonpath='{.data.admin\.password}' 2>/dev/null | base64 -d || true)
f=$(mktemp)
printf 'admin:%s\n' "${lama/#\$2a/\$2y}" >"$f"
if [ -z "$lama" ] || ! htpasswd -vb "$f" admin "$ARGOCD_ADMIN_PASSWORD" >/dev/null 2>&1; then
  hash=$(htpasswd -nbBC 10 "" "$ARGOCD_ADMIN_PASSWORD" | tr -d ':\n' | sed 's/^\$2y/$2a/')
  "${K[@]}" -n argocd patch secret argocd-secret --type merge -p "$(jq -cn --arg h "$hash" \
    --arg t "$(date -u +%FT%TZ)" '{stringData: {"admin.password": $h, "admin.passwordMtime": $t}}')" >/dev/null
  "${K[@]}" -n argocd delete secret argocd-initial-admin-secret --ignore-not-found >/dev/null
  log "password admin Argo CD disamakan dengan .env"
fi
rm -f "$f"
log "manifest Argo CD $ARGOCD_VERSI diterapkan; menunggu pod (tarik image pertama kali 1-3 menit)"
for obj in deploy/argocd-redis deploy/argocd-repo-server deploy/argocd-server statefulset/argocd-application-controller; do
  "${K[@]}" -n argocd rollout status "$obj" --timeout=600s >/dev/null || gagal "$obj tidak siap"
done

masuk_argocd() {
  curl -sf -H 'content-type: application/json' "$A/api/v1/session" \
    -d "$(jq -cn --arg p "$ARGOCD_ADMIN_PASSWORD" '{username: "admin", password: $p}')" | jq -e .token
}
tunggu "argocd-server lewat NodePort" 120 curl -sf "$A/healthz"
tunggu "login admin Argo CD" 120 masuk_argocd

"${K[@]}" apply -f "$W/application.yaml" >/dev/null
rm -rf "$W"
log "Application app dibuat: Argo CD mengawasi $G/$OWNER/gitops.git (folder app/)"
log "init selesai"
