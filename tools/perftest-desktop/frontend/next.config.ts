import type { NextConfig } from "next";

// Frontend Wails: static export (folder out/) yang di-embed ke binary Go.
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
  trailingSlash: true,
  devIndicators: false,
};

export default nextConfig;
