import type { NextConfig } from "next";

const config: NextConfig = {
  output: "export",
  poweredByHeader: false,
  productionBrowserSourceMaps: false,
  experimental: { cpus: 2 },
};

export default config;
