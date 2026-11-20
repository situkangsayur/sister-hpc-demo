// Proxy dari browser ke service pantau (Go). Alamat dibaca saat runtime (PANTAU_URL) sehingga
// image UI yang sama dapat dipakai di mana saja. Credential Jenkins/Gitea/Argo CD hanya ada di pantau.
export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const BOLEH = /^(status|push|rollback|drift)$/;

async function teruskan(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const jalur = path.join("/");
  if (!BOLEH.test(jalur) || (req.method === "POST") === (jalur === "status")) {
    return Response.json({ error: "jalur tidak dikenal" }, { status: 404 });
  }
  const base = process.env.PANTAU_URL ?? "http://pantau:8080";
  try {
    const up = await fetch(`${base}/api/${jalur}`, {
      method: req.method,
      headers: { "content-type": "application/json" },
      body: req.method === "POST" ? await req.text() : undefined,
      signal: req.signal,
      cache: "no-store",
    });
    return new Response(up.body, {
      status: up.status,
      headers: { "content-type": up.headers.get("content-type") ?? "application/json", "cache-control": "no-store" },
    });
  } catch {
    return Response.json({ error: "pantau tidak dapat dihubungi" }, { status: 502 });
  }
}

export const GET = teruskan;
export const POST = teruskan;
