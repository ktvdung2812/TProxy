import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backend = "http://127.0.0.1:28120";
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
    // Bind IPv4 explicitly so localhost/127.0.0.1 both reach Vite (avoids ::1-only listen).
    host: "127.0.0.1",
    port: 28121,
    strictPort: true,
    // base is /dashboard/; pin HMR to the Vite server so the client does not
    // attempt a websocket against the SPA path under /dashboard/.
    hmr: {
      protocol: "ws",
      host: "127.0.0.1",
      port: 28121,
      clientPort: 28121,
    },
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/healthz": { target: backend, changeOrigin: true },
      "/v1": { target: backend, changeOrigin: true },
      "/v1beta": { target: backend, changeOrigin: true },
    },
  },
});
