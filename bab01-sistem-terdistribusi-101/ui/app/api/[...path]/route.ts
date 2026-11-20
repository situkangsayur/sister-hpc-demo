// Proxy dari browser ke service monitor. Alamat monitor dibaca saat runtime (MONITOR_URL),
// sehingga image UI yang sama bisa dipakai di mana saja. SSE (/api/stream) diteruskan apa adanya.
export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const BOLEH = /^(status|stream|ping|lalu-lintas|reset|fault\/node[1-9]|crash\/node[1-9]|ukur\/(rtt|throughput|bandingkan))$/;

async function teruskan(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const jalur = path.join("/");
  if (!BOLEH.test(jalur)) {
    return Response.json({ error: "jalur tidak dikenal" }, { status: 404 });
  }
  const base = process.env.MONITOR_URL ?? "http://monitor:8080";
  try {
    const up = await fetch(`${base}/api/${jalur}`, {
      method: req.method,
      headers: { "content-type": "application/json" },
      body: req.method === "POST" ? await req.text() : undefined,
      signal: req.signal,
      cache: "no-store",
    });
    const headers = new Headers({
      "content-type": up.headers.get("content-type") ?? "application/json",
      "cache-control": "no-cache, no-store",
    });
    if (jalur === "stream") headers.set("x-accel-buffering", "no");
    return new Response(up.body, { status: up.status, headers });
  } catch {
    return Response.json({ error: "monitor tidak dapat dihubungi" }, { status: 502 });
  }
}

export const GET = teruskan;
export const POST = teruskan;
