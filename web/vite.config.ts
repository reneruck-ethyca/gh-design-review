import { defineConfig } from "vitest/config";
import preact from "@preact/preset-vite";

export default defineConfig({
  plugins: [preact()],
  // ponytail: the only chunks over 500 kB are mermaid's lazy ones (elk ~1.5 MB), served from localhost
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 1600 },
  test: { environment: "jsdom" },
});
