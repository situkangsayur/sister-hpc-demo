// Uji rate limiting APISIX: kirim N request pencarian sekaligus, hitung status yang kembali.
export const dynamic = "force-dynamic";

const GATEWAY = process.env.GATEWAY_URL ?? "http://apisix:9080";

export async function POST(req: Request) {
  const { n = 30 } = (await req.json().catch(() => ({}))) as { n?: number };
  const count = Math.min(Math.max(n, 1), 60);
  const results = await Promise.all(
    Array.from({ length: count }, () =>
      fetch(`${GATEWAY}/api/catalog/search?q=kopi`, { cache: "no-store", signal: AbortSignal.timeout(10000) })
        .then((r) => r.status)
        .catch(() => 0),
    ),
  );
  const byStatus: Record<string, number> = {};
  for (const s of results) byStatus[String(s)] = (byStatus[String(s)] ?? 0) + 1;
  return Response.json({ sent: count, by_status: byStatus });
}
