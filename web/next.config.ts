import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  experimental: {
    // Server actions (Play) may only come from the loopback origins the demo runs on.
    serverActions: { allowedOrigins: ["localhost:3000", "127.0.0.1:3000"] },
  },
};

export default nextConfig;
