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
      // "localhost" resolves to ::1 first on macOS while the dev server binds a
      // single IPv4 address, so the HMR socket is refused even though the page
      // itself loads. Point the client at the address actually bound; when the
      // server listens on every interface, let it derive the host from the page.
      // `path` is intentionally unset: Vite joins it onto `base`, so setting it
      // to "/dashboard/" produced the /dashboard/dashboard/ socket URL.
      host: devBindHost === "0.0.0.0" ? undefined : devBindHost,
      port: publicPort,
      clientPort: publicPort,
      // Prevent full-page reload when HMR fails; show overlay instead.
      overlay: true,
    },
    // Exclude non-frontend files from the file watcher to reduce
    // unnecessary HMR events that can trigger full-page reloads.
    watch: {
      ignored: ["**/../internal/**", "**/../cmd/**", "**/../tunnel/**", "**/../deploy/**", "**/../.git/**", "**/../tproxy.db*"],
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
