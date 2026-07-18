import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backend = "http://127.0.0.1:28120";

export default defineConfig({
  plugins: [react()],
  base: "/dashboard/",
  build: {
    outDir: "../internal/api/dashboard",
    emptyOutDir: true,
  },
  server: {
    port: 28121,
    strictPort: true,
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/healthz": { target: backend, changeOrigin: true },
      "/v1": { target: backend, changeOrigin: true },
      "/v1beta": { target: backend, changeOrigin: true },
    },
  },
});
