// Pembantu kecil untuk memanggil probe lewat route /api/probe.
export async function probe<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/probe/${path}`, { cache: "no-store", ...init });
  const data = await res.json();
  if (!res.ok && data && typeof data === "object" && "err" in data) {
    throw new Error(String((data as { err: string }).err));
  }
  return data as T;
}

export function postJSON(body: unknown): RequestInit {
  return { method: "POST", body: JSON.stringify(body), headers: { "content-type": "application/json" } };
}

export const fmt = (x: number, d = 1) => (Number.isFinite(x) ? x.toFixed(d) : "-");
