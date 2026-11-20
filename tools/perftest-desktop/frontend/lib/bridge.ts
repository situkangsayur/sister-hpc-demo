// Jembatan ke backend Go lewat binding Wails (window.go.main.App.*) dan event runtime.
// Wails menyuntikkan /wails/ipc.js dan /wails/runtime.js ke index.html secara otomatis.
import type { Config, GRPCMethodInfo, Point, Preset, Report } from "./types";

interface AppBinding {
  Presets(): Promise<Preset[]>;
  LoadPresetFile(): Promise<Preset[]>;
  DefaultHost(): Promise<string>;
  Start(cfg: Config, host: string): Promise<void>;
  Stop(): Promise<void>;
  GRPCMethods(target: string, tls: boolean, insecure: boolean, host: string): Promise<GRPCMethodInfo[]>;
  Export(format: "csv" | "json"): Promise<string>;
  Version(): Promise<string>;
  InitialConfig(): Promise<{ config: Config; autostart: boolean } | null>;
}

interface WailsWindow {
  go?: { main?: { App?: AppBinding } };
  runtime?: { EventsOn(name: string, cb: (...data: unknown[]) => void): () => void };
}

function w(): WailsWindow {
  return (typeof window === "undefined" ? {} : window) as unknown as WailsWindow;
}

export function inWails(): boolean {
  return !!w().go?.main?.App;
}

export function app(): AppBinding {
  const a = w().go?.main?.App;
  if (!a) throw new Error("Backend Go tidak tersedia: jalankan lewat aplikasi desktop perftest-desktop.");
  return a;
}

export function on<T>(name: string, cb: (v: T) => void): () => void {
  const rt = w().runtime;
  if (!rt) return () => {};
  return rt.EventsOn(name, (v) => cb(v as T));
}

export type { Point, Report };
