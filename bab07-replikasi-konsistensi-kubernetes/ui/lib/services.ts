// Peta nama service (dipakai di URL /api/<svc>/...) ke alamat internal di jaringan compose.
// Nilainya dapat ditimpa lewat environment variable saat runtime.
export const SERVICES: Record<string, { env: string; def: string; profile: string }> = {
  "clock-p1": { env: "CLOCK_P1_URL", def: "http://clock-p1:8080", profile: "clock" },
  "clock-p2": { env: "CLOCK_P2_URL", def: "http://clock-p2:8080", profile: "clock" },
  "clock-p3": { env: "CLOCK_P3_URL", def: "http://clock-p3:8080", profile: "clock" },
  repl: { env: "REPL_URL", def: "http://repl-api:8080", profile: "replication" },
  "raft-n1": { env: "RAFT_N1_URL", def: "http://raft-n1:8080", profile: "consensus" },
  "raft-n2": { env: "RAFT_N2_URL", def: "http://raft-n2:8080", profile: "consensus" },
  "raft-n3": { env: "RAFT_N3_URL", def: "http://raft-n3:8080", profile: "consensus" },
  search: { env: "SEARCH_URL", def: "http://search-api:8080", profile: "search" },
  k8s: { env: "K8S_URL", def: "http://k8s-watch:8080", profile: "k8s" },
};
