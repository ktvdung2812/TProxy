import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const publicPort = 28120;
const backendPort = 28122;
const backend = `http://127.0.0.1:${backendPort}`;
const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function readRunEnv(name: string): string {
  try {
    const text = readFileSync(resolve(rootDir, ".env.run"), "utf8");
    const match = text.match(new RegExp(`export ${name}="([^"]*)"`));
    return match?.[1] ?? "";
  } catch {
    return "";
  }
}

const runApiKey = readRunEnv("TPROXY_API_KEY");
const runManagementSecret = readRunEnv("TPROXY_MANAGEMENT_SECRET");

export default defineConfig({
  plugins: [react()],
  base: "/dashboard/",
  define: {
    "import.meta.env.VITE_TPROXY_API_KEY": JSON.stringify(runApiKey),
    "import.meta.env.VITE_TPROXY_MANAGEMENT_SECRET": JSON.stringify(runManagementSecret),
  },
  build: {
    outDir: "../internal/api/dashboard",
    emptyOutDir: true,
  },
  server: {
    host: "0.0.0.0",
    port: publicPort,
    strictPort: true,
    hmr: {
      protocol: "ws",
      host: "localhost",
      port: publicPort,
      clientPort: publicPort,
      path: "/dashboard/",
    },
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/healthz": { target: backend, changeOrigin: true },
      "/v1": { target: backend, changeOrigin: true },
      "/v1beta": { target: backend, changeOrigin: true },
    },
  },
});
