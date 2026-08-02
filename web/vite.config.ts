import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const publicPort = 28120;
const backendPort = 28122;
const backend = `http://127.0.0.1:${backendPort}`;
const devBindHost = process.env.TPROXY_DEV_BIND_HOST || "127.0.0.1";

export default defineConfig({
  plugins: [react()],
  base: "/dashboard/",
  build: {
    outDir: "../internal/api/dashboard",
    emptyOutDir: true,
  },
  server: {
    // cloudflared connects to this loopback listener. Set
    // TPROXY_DEV_BIND_HOST=0.0.0.0 only when LAN access is intentional.
    host: devBindHost,
    port: publicPort,
    strictPort: true,
    // The direct Cloudflare quick-tunnel hostname is forwarded as Host.
    allowedHosts: [".trycloudflare.com", "localhost", "127.0.0.1"],
    hmr: {
      protocol: "ws",
      host: "localhost",
      port: publicPort,
      clientPort: publicPort,
      path: "/dashboard/",
    },
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/callback": { target: backend, changeOrigin: true },
      "/healthz": { target: backend, changeOrigin: true },
      "/v1": { target: backend, changeOrigin: true },
      "/v1beta": { target: backend, changeOrigin: true },
    },
  },
});
