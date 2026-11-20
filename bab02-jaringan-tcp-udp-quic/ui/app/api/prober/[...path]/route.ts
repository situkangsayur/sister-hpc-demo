// Proxy server-side ke service prober (browser tidak perlu tahu alamat internal).
import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const allowed = new Set(["status", "netem", "run", "history", "hol", "trace", "ss"]);

async function forward(req: NextRequest, path: string[], method: "GET" | "POST") {
  const target = path.join("/");
  if (path.length !== 1 || !allowed.has(target)) {
    return Response.json({ error: "endpoint tidak dikenal" }, { status: 404 });
  }
  const base = process.env.PROBER_URL ?? "http://prober:8080";
  try {
    const res = await fetch(`${base}/api/${target}`, {
      method,
      headers: { "Content-Type": "application/json" },
      body: method === "POST" ? await req.text() : undefined,
      cache: "no-store",
      signal: AbortSignal.timeout(150_000),
    });
    return new Response(await res.text(), {
      status: res.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (e) {
    return Response.json({ error: `prober tidak dapat dihubungi: ${String(e)}` }, { status: 502 });
  }
}

export async function GET(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return forward(req, (await ctx.params).path, "GET");
}

export async function POST(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return forward(req, (await ctx.params).path, "POST");
}
