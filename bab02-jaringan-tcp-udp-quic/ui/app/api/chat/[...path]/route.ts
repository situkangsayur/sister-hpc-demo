// Proxy server-side ke API HTTP service chat (daftar client dan riwayat).
import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

export async function GET(_req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const target = path.join("/");
  if (target !== "clients" && target !== "history") {
    return Response.json({ error: "endpoint tidak dikenal" }, { status: 404 });
  }
  const base = process.env.CHAT_API_URL ?? "http://chat:8090";
  try {
    const res = await fetch(`${base}/api/${target}`, { cache: "no-store", signal: AbortSignal.timeout(5000) });
    return new Response(await res.text(), { status: res.status, headers: { "Content-Type": "application/json" } });
  } catch (e) {
    return Response.json({ error: `chat tidak dapat dihubungi: ${String(e)}` }, { status: 502 });
  }
}
