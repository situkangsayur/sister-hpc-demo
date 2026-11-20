// Proxy same-origin dari browser ke service klien (Go). Body response diteruskan
// sebagai stream sehingga SSE (/api/grpc/pantau) tetap mengalir.
import type { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const KLIEN = process.env.KLIEN_URL ?? "http://localhost:8080";

async function teruskan(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const url = `${KLIEN}/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const isi = req.method === "GET" || req.method === "HEAD" ? undefined : await req.arrayBuffer();
  try {
    const resp = await fetch(url, {
      method: req.method,
      headers: { "content-type": req.headers.get("content-type") ?? "application/json" },
      body: isi,
      signal: req.signal,
      cache: "no-store",
    });
    const h = new Headers();
    h.set("content-type", resp.headers.get("content-type") ?? "application/json");
    h.set("cache-control", "no-cache");
    return new Response(resp.body, { status: resp.status, headers: h });
  } catch (e) {
    return Response.json({ error: `klien tidak terjangkau: ${(e as Error).message}` }, { status: 502 });
  }
}

export const GET = teruskan;
export const POST = teruskan;
export const PUT = teruskan;
