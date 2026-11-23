// Proxy server-side dari UI ke APISIX. Browser tidak pernah melihat API key: UI berperan
// sebagai klien "storefront" yang terdaftar di APISIX (consumer key-auth).
import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const GATEWAY = process.env.GATEWAY_URL ?? "http://apisix:9080";
const KEY = process.env.APISIX_KEY ?? "";
const PASS_HEADERS = ["x-request-id", "x-served-by", "x-upstream", "x-ratelimit-limit", "x-ratelimit-remaining", "x-ratelimit-reset"];

async function proxy(req: NextRequest, path: string[]) {
  const url = new URL(req.url);
  const noAuth = url.searchParams.get("noauth") === "1";
  url.searchParams.delete("noauth");
  const target = `${GATEWAY}/api/${path.map(encodeURIComponent).join("/")}${url.search}`;
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (KEY && !noAuth) headers["apikey"] = KEY;
  const idem = req.headers.get("idempotency-key");
  if (idem) headers["idempotency-key"] = idem;
  const started = Date.now();
  try {
    const res = await fetch(target, {
      method: req.method,
      headers,
      body: req.method === "GET" ? undefined : await req.text(),
      cache: "no-store",
      signal: AbortSignal.timeout(15000),
    });
    const body = await res.text();
    const out = new Headers({ "content-type": res.headers.get("content-type") ?? "application/json" });
    for (const h of PASS_HEADERS) {
      const v = res.headers.get(h);
      if (v) out.set(h, v);
    }
    out.set("x-gateway-ms", String(Date.now() - started));
    return new Response(body, { status: res.status, headers: out });
  } catch (e) {
    return Response.json({ error: `gateway tidak terjangkau: ${String(e)}` }, { status: 502 });
  }
}

export async function GET(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return proxy(req, (await ctx.params).path);
}

export async function POST(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return proxy(req, (await ctx.params).path);
}

export async function PATCH(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  return proxy(req, (await ctx.params).path);
}
