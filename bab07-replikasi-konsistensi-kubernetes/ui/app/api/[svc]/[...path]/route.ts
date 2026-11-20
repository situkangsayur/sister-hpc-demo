import { NextRequest } from "next/server";
import { SERVICES } from "@/lib/services";

export const dynamic = "force-dynamic";

// Proxy tipis dari browser ke service Go di jaringan compose. Bila service tidak jalan
// (profilnya belum dinyalakan), jawab 503 {down: true} agar halaman menampilkan petunjuk.
async function forward(req: NextRequest, ctx: { params: Promise<{ svc: string; path: string[] }> }) {
  const { svc, path } = await ctx.params;
  const s = SERVICES[svc];
  if (!s) return Response.json({ error: "service tidak dikenal" }, { status: 404 });
  const base = process.env[s.env] || s.def;
  const url = `${base}/${path.join("/")}${req.nextUrl.search}`;
  try {
    const init: RequestInit = {
      method: req.method,
      headers: { "content-type": "application/json" },
      cache: "no-store",
      signal: AbortSignal.timeout(25000),
    };
    if (req.method !== "GET" && req.method !== "HEAD") init.body = await req.text();
    const r = await fetch(url, init);
    const body = await r.text();
    return new Response(body, { status: r.status, headers: { "content-type": r.headers.get("content-type") || "application/json" } });
  } catch (e) {
    return Response.json({ down: true, svc, profile: s.profile, error: String(e) }, { status: 503 });
  }
}

export { forward as GET, forward as POST };
