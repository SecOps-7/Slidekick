import { defineConfig } from "vite";

// The Go server embeds web/dist. In dev, `npm run dev` proxies API, WebSocket
// and theme requests to a slidekick server on :7410.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const backend = env.SLIDEKICK_DEV_BACKEND ?? "http://localhost:7410";

export default defineConfig({
  build: { outDir: "dist", emptyOutDir: true, target: "es2022", sourcemap: true },
  server: {
    fs: { allow: [".."] },
    proxy: {
      "/api": backend,
      "/themes": backend,
      "/schema": backend,
      "/mcp": backend,
      "/ws": { target: backend.replace(/^http/, "ws"), ws: true },
    },
  },
});
