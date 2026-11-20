import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // Kompresi dimatikan agar Server-Sent Events tidak tertahan di buffer gzip.
  compress: false,
};

export default nextConfig;
