"use client";
import { useCallback, useEffect, useRef, useState } from "react";

export type Poll<T> = { data: T | null; down: boolean; error: string; refresh: () => void };

// usePoll mengambil url tiap `ms` milidetik. down=true bila service (profil compose) tidak jalan.
export function usePoll<T>(url: string, ms = 1000): Poll<T> {
  const [data, setData] = useState<T | null>(null);
  const [down, setDown] = useState(false);
  const [error, setError] = useState("");
  const alive = useRef(true);
  const load = useCallback(async () => {
    try {
      const r = await fetch(url, { cache: "no-store" });
      const j = await r.json();
      if (!alive.current) return;
      if (r.status === 503 && j.down) {
        setDown(true);
        return;
      }
      setDown(false);
      setError(r.ok ? "" : j.error || `HTTP ${r.status}`);
      if (r.ok) setData(j as T);
    } catch (e) {
      if (alive.current) setError(String(e));
    }
  }, [url]);
  useEffect(() => {
    alive.current = true;
    load();
    const t = setInterval(load, ms);
    return () => {
      alive.current = false;
      clearInterval(t);
    };
  }, [load, ms]);
  return { data, down, error, refresh: load };
}

// post mengirim JSON dan mengembalikan body; error dilempar dengan pesan dari service.
export async function post<T = unknown>(url: string, body: unknown = {}): Promise<T> {
  const r = await fetch(url, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
  return j as T;
}

export function fmtTime(ms: number): string {
  const d = new Date(ms);
  return d.toLocaleTimeString("id-ID", { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
}
