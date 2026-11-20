// Tipe yang sama dengan struct Go di package engine dan presets.
export type Kind = "http" | "graphql" | "grpc" | "ws";

export interface Config {
  name: string;
  kind: Kind;
  url?: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  query?: string;
  variables?: string;
  target?: string;
  grpcMethod?: string;
  request?: string;
  tls?: boolean;
  message?: string;
  noReply?: boolean;
  concurrency: number;
  duration?: string;
  requests?: number;
  rps?: number;
  timeout?: string;
  insecureSkipVerify?: boolean;
}

export interface Point {
  t: number;
  rps: number;
  ok: number;
  errors: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
  meanMs: number;
  errorsByKind?: Record<string, number>;
  total: number;
  errSum: number;
}

export interface Bin {
  fromMs: number;
  toMs: number;
  count: number;
}

export interface Report {
  config: Config;
  startedAt: string;
  elapsedS: number;
  total: number;
  ok: number;
  errors: number;
  errorsByKind: Record<string, number>;
  rps: number;
  latency: { minMs: number; meanMs: number; p50Ms: number; p90Ms: number; p95Ms: number; p99Ms: number; maxMs: number };
  timeline: Point[];
  histogram: Bin[];
  stopped: boolean;
}

export interface Preset {
  bab: number;
  name: string;
  config: Config;
}

export interface GRPCMethodInfo {
  method: string;
  input: string;
  output: string;
  example: string;
}
