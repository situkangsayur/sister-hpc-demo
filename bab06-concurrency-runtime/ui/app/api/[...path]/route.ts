// Proxy runtime dari browser ke service lab (Go). Alamat lab dibaca saat runtime dari LAB_URL,
// sehingga image UI yang sama bisa dipakai di laptop maupun server lab. Body diteruskan sebagai
// stream, termasuk Server-Sent Events.
import type { NextRequest } from "next/server";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const base = process.env.LAB_URL ?? "http://localhost:8080";
  const url = `${base}/api/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const init: RequestInit = {
    method: req.method,
    headers: { "content-type": req.headers.get("content-type") ?? "application/json" },
    signal: req.signal,
    cache: "no-store",
  };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = await req.text();
  }
  try {
    const res = await fetch(url, init);
    return new Response(res.body, {
      status: res.status,
      headers: {
        "content-type": res.headers.get("content-type") ?? "application/json",
        "cache-control": "no-cache, no-transform",
        "x-accel-buffering": "no",
      },
    });
  } catch (e) {
    return Response.json({ error: `lab tidak terjangkau: ${String(e)}` }, { status: 502 });
  }
}

export const GET = proxy;
export const POST = proxy;
