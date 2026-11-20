import { defineConfig } from "vite";

// Builds the self-contained player used by HTML export: one IIFE with every
// lazy chunk (Shiki, Mermaid) inlined, plus one CSS file. Output goes next to
// the main app in dist/standalone so the Go binary embeds both.
//
// SK_LITE=1 builds slidekick-lite.js with Mermaid stubbed out (about a fifth
// of the size); the exporter uses it for decks without diagrams.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const lite = env.SK_LITE === "1";

export default defineConfig({
  resolve: lite ? { alias: { mermaid: new URL("./runtime/mermaid-stub.ts", import.meta.url).pathname } } : {},
  build: {
    outDir: "dist/standalone",
    emptyOutDir: !lite,
    target: "es2022",
    cssCodeSplit: false,
    sourcemap: false,
    lib: {
      entry: "src/standalone.ts",
      name: "Slidekick",
      formats: ["iife"],
      fileName: () => (lite ? "slidekick-lite.js" : "slidekick.js"),
      cssFileName: "slidekick",
    },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
});
