// Meneruskan request UI ke service probe. Browser hanya berbicara dengan UI (port 50300),
// sehingga tidak pernah menyentuh certificate self-signed demo.
import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const PROBE = process.env.PROBE_URL ?? "http://localhost:8090";

async function forward(req: NextRequest, path: string[]) {
  const url = `${PROBE}/api/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const init: RequestInit = { method: req.method, headers: { "content-type": "application/json" }, cache: "no-store" };
  if (req.method === "POST") init.body = await req.text();
  try {
    const res = await fetch(url, init);
    return new Response(await res.text(), {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (e) {
    return Response.json({ err: `probe tidak dapat dihubungi: ${String(e)}` }, { status: 502 });
  }
}

export async function GET(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return forward(req, (await ctx.params).path);
}

export async function POST(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return forward(req, (await ctx.params).path);
}
