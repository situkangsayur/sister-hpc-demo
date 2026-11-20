"use client";

import { useCallback, useEffect, useRef, useState } from "react";

export async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data?.error ?? `HTTP ${res.status}`);
  return data as T;
}

/** Mengubah knob atau memicu aksi pada stream yang sedang berjalan. */
export async function setKnob(session: string | null, params: Record<string, number>) {
  if (!session) return;
  const q = new URLSearchParams(Object.entries(params).map(([k, v]) => [k, String(v)]));
  await fetch(`/api/session/${session}?${q}`, { method: "POST" });
}

type Handlers = Record<string, (data: unknown) => void>;

/** useStream membuka EventSource ke /api/<path> dan memanggil handler per nama event. */
export function useStream(handlers: Handlers) {
  const esRef = useRef<EventSource | null>(null);
  const hRef = useRef(handlers);
  hRef.current = handlers;
  const [running, setRunning] = useState(false);

  const stop = useCallback(() => {
    esRef.current?.close();
    esRef.current = null;
    setRunning(false);
  }, []);

  const start = useCallback(
    (path: string) => {
      esRef.current?.close();
      const es = new EventSource(`/api/${path}`);
      esRef.current = es;
      setRunning(true);
      const names = Object.keys(hRef.current);
      for (const n of names) {
        es.addEventListener(n, (e) => {
          const data = JSON.parse((e as MessageEvent).data);
          hRef.current[n]?.(data);
          if (n === "done") stop();
        });
      }
      es.onerror = () => stop(); // server menutup stream
    },
    [stop],
  );

  useEffect(() => () => esRef.current?.close(), []);
  return { running, start, stop };
}

export type LogLine = { t_ms: number; msg: string };

export function fmtMs(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${ms.toFixed(1)} ms`;
}

export function fmtBytes(b: number) {
  if (Math.abs(b) >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (Math.abs(b) >= 1024) return `${(b / 1024).toFixed(1)} KB`;
  return `${b.toFixed(0)} B`;
}

export const COLORS = ["var(--s1)", "var(--s2)", "var(--s3)", "var(--s4)", "var(--s5)"];
