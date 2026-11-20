import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  compress: false, // SSE tidak boleh ditahan oleh kompresi
};

export default nextConfig;
