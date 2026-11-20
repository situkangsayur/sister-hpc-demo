#!/usr/bin/env bash
# Uji otomatis Bab 2: build, tunggu sehat, panggil endpoint utama, cek hasil, lalu down -v.
set -euo pipefail
cd "$(dirname "$0")"
[ -f .env ] || cp .env.example .env
set -a; . ./.env; set +a
H="${BIND_ADDR:-127.0.0.1}"
UI="http://$H:50200"
API="http://$H:50201/api"

avail_kb=$(df -Pk / | awk 'NR==2 {print $4}')
if [ "$avail_kb" -lt $((8 * 1024 * 1024)) ]; then
  echo "SISA DISK < 8 GB ($((avail_kb / 1024)) MB): build ditunda" >&2
  exit 2
fi

cleanup() { docker compose down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

lulus=0
ok() { echo "  OK   $*"; lulus=$((lulus + 1)); }
gagal() { echo "  GAGAL $*" >&2; docker compose ps >&2 || true; docker compose logs --tail 30 >&2 || true; exit 1; }
need() { command -v "$1" >/dev/null || { echo "butuh $1" >&2; exit 1; }; }
need curl; need python3; need nc
py() { python3 -c "$1"; }

echo "== build dan start"
log=$(mktemp)
docker compose build >"$log" 2>&1 || { tail -40 "$log" >&2; rm -f "$log"; gagal "compose build"; }
docker compose up -d --wait --wait-timeout 180 >"$log" 2>&1 || { tail -20 "$log" >&2; rm -f "$log"; gagal "compose up --wait"; }
rm -f "$log"
ok "semua container healthy"

echo "== UI"
for p in / /hol /socket /chat; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$UI$p")
  [ "$code" = 200 ] || gagal "UI $p -> $code"
done
ok "UI melayani /, /hol, /socket, /chat (HTTP 200)"
curl -sf "$UI/api/config" | py 'import json,sys; assert json.load(sys.stdin)["chatWsPort"]' || gagal "/api/config"
ok "UI /api/config"

echo "== echo dari host (nc)"
[ "$(echo halo-tcp | nc -q1 -w3 "$H" 50220)" = "halo-tcp" ] || gagal "echo TCP lewat nc"
[ "$(echo halo-udp | nc -u -w1 "$H" 50221)" = "halo-udp" ] || gagal "echo UDP lewat nc -u"
ok "echo TCP dan UDP menjawab nc"

echo "== netem lewat UI (proxy ke prober -> router)"
curl -sf -XPOST "$UI/api/prober/netem" -d '{"delay_ms":25,"loss_pct":0}' \
  | py 'import json,sys; d=json.load(sys.stdin); assert d["params"]["delay_ms"]==25 and "netem" in d["c2s"]["tc"], d' \
  || gagal "pasang netem"
ok "netem delay 25ms terpasang di kedua arah"

echo "== putaran perbandingan transport"
curl -sf -XPOST "$UI/api/prober/run" -d '{"count":30,"interval_ms":5,"bulk_kb":256}' >/dev/null || gagal "mulai run"
for _ in $(seq 1 90); do
  st=$(curl -sf "$UI/api/prober/run" | py 'import json,sys; print(json.load(sys.stdin)["status"])')
  [ "$st" = done ] && break
  sleep 1
done
[ "$st" = done ] || gagal "run tidak selesai"
curl -sf "$UI/api/prober/run" | py '
import json,sys
r=json.load(sys.stdin)
for p in r["protocols"]:
    assert p["status"]=="done", p
    assert p["received"]==30, (p["proto"], p["received"])
    # delay 25 ms di dua arah -> RTT >= 50 ms
    assert 49 <= p["stats"]["median"] < 200, (p["proto"], p["stats"])
    assert p["throughput"] and p["throughput"]["mbps"]>0, p["throughput"]
hs={h["name"]:h for h in r["handshakes"]}
tcp=hs["TCP"]["handshake_ms"]; tls=hs["TCP + TLS 1.3"]["handshake_ms"]; q=hs["QUIC (baru)"]["handshake_ms"]; z=hs["QUIC 0-RTT (koneksi ulang)"]["handshake_ms"]
assert 45<tcp<120 and 95<tls<220 and 45<q<150 and z<40, (tcp,tls,q,z)
print("     median RTT tcp/udp/quic:", [p["stats"]["median"] for p in r["protocols"]], "handshake tcp/tls/quic/0rtt:", tcp, tls, q, z)
' || gagal "hasil run"
ok "TCP/UDP/QUIC: 30/30 balasan, RTT ~2x delay, handshake 1/2/1/0 RTT, throughput > 0"
curl -sf "$UI/api/prober/history" | py 'import json,sys; assert len(json.load(sys.stdin))>=1' || gagal "history"
ok "riwayat putaran"

echo "== loss: UDP kehilangan message, TCP tidak"
curl -sf -XPOST "$API/netem" -d '{"delay_ms":5,"loss_pct":20}' >/dev/null || gagal "netem loss"
curl -sf -XPOST "$API/run" -d '{"count":60,"interval_ms":2,"bulk_kb":0}' >/dev/null
for _ in $(seq 1 120); do
  st=$(curl -sf "$API/run" | py 'import json,sys; print(json.load(sys.stdin)["status"])')
  [ "$st" = done ] && break
  sleep 1
done
curl -sf "$API/run" | py '
import json,sys
r={p["proto"]:p for p in json.load(sys.stdin)["protocols"]}
assert r["tcp"]["received"]==60 and r["quic"]["received"]==60, (r["tcp"]["received"], r["quic"]["received"])
assert r["udp"]["loss_pct"]>5, r["udp"]["loss_pct"]
print("     UDP hilang", r["udp"]["loss_pct"], "% ; TCP maks", r["tcp"]["stats"]["max"], "ms vs median", r["tcp"]["stats"]["median"])
' || gagal "uji loss"
ok "loss 20%: TCP dan QUIC 60/60, UDP kehilangan message"

echo "== trace satu koneksi TCP + tcpdump dua sisi"
curl -sf -XPOST "$API/netem" -d '{"delay_ms":20,"loss_pct":0}' >/dev/null
curl -sf -XPOST "$UI/api/prober/trace" -d '{"proto":"tcp","messages":2}' | py '
import json,sys
r=json.load(sys.stdin)
kinds=[f["kind"] for f in r["flows"]]
assert kinds[:2]==["SYN","SYN-ACK"], kinds
assert "FIN" in kinds and kinds.count("DATA")==4, kinds
assert all(f["send_ts"] and f["recv_ts"] for f in r["flows"]), "tanpa loss semua packet harus tiba"
calls=[e["call"] for e in r["events"]]
for c in ["connect()","accept()","send()","recv()","close()"]:
    assert c in calls, (c, calls)
print("     packet:", " ".join(kinds))
' || gagal "trace tcp"
ok "trace TCP: SYN, SYN-ACK, DATA, FIN terlihat di dua sisi; event socket API lengkap"
curl -sf -XPOST "$API/trace" -d '{"proto":"quic","messages":1}' \
  | py 'import json,sys; r=json.load(sys.stdin); k=[f["kind"] for f in r["flows"]]; assert k and k[0]=="QUIC Initial", k' || gagal "trace quic"
ok "trace QUIC: packet Initial terdeteksi"

echo "== head-of-line blocking"
curl -sf -XPOST "$API/netem" -d '{"delay_ms":20,"loss_pct":5}' >/dev/null
curl -sf -XPOST "$UI/api/prober/hol" -d '{"streams":3,"messages":40,"interval_ms":20}' | py '
import json,sys
r={x["transport"]:x for x in json.load(sys.stdin)["results"]}
for t in ("tcp","quic"):
    assert not r[t].get("error") and len(r[t]["samples"])==120, (t, r[t].get("error"))
print("     tersendat bersamaan: tcp", r["tcp"]["shared_pct"], "% , quic", r["quic"]["shared_pct"], "%")
' || gagal "hol"
ok "uji HOL TCP dan QUIC mengembalikan 120 sampel"
curl -sf -XPOST "$API/netem" -d '{}' >/dev/null

echo "== socket chat: nc dan WebSocket di ruang yang sama"
( printf '/nick uji-nc\nhalo dari nc\n'; sleep 2; printf '/quit\n' ) | nc -w5 "$H" 50210 > /tmp/bab02-chat.$$ &
sleep 0.8
curl -sf "$UI/api/chat/clients" | py 'import json,sys; c=json.load(sys.stdin); assert any(x["nick"]=="uji-nc" and x["kind"]=="tcp" for x in c), c' || gagal "client nc tidak terdaftar"
ok "client nc terlihat di /api/chat/clients"
python3 - "$H" <<'PYWS' || gagal "websocket"
import base64, os, socket, sys
h = sys.argv[1]
s = socket.create_connection((h, 50202), timeout=5)
key = base64.b64encode(os.urandom(16)).decode()
s.sendall(f"GET /ws?nick=uji-web HTTP/1.1\r\nHost: {h}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n".encode())
buf = b""
while b"\r\n\r\n" not in buf:
    buf += s.recv(4096)
assert b" 101 " in buf.split(b"\r\n")[0], buf[:80]
payload = b"halo dari websocket"
mask = os.urandom(4)
s.sendall(bytes([0x81, 0x80 | len(payload)]) + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(payload)))
data = buf.split(b"\r\n\r\n", 1)[1]
s.settimeout(3)
try:
    while b"uji-web: halo dari websocket" not in data:
        chunk = s.recv(4096)
        if not chunk:
            break
        data += chunk
except socket.timeout:
    pass
assert b"uji-web: halo dari websocket" in data, data[-300:]
PYWS
wait
grep -q "uji-web: halo dari websocket" /tmp/bab02-chat.$$ || { cat /tmp/bab02-chat.$$; rm -f /tmp/bab02-chat.$$; gagal "nc tidak menerima pesan dari WebSocket"; }
rm -f /tmp/bab02-chat.$$
ok "pesan dari WebSocket diterima client nc (gateway WS <-> TCP)"

echo "== log JSON"
plog=$(docker compose logs --no-log-prefix prober 2>/dev/null)
grep '"msg":"putaran selesai"' <<<"$plog" | head -n1 | py 'import json,sys; d=json.loads(sys.stdin.read()); assert d["service"]=="prober"' || gagal "log JSON"
ok "log JSON satu baris dengan field service"

echo "SEMUA LULUS ($lulus cek)"
