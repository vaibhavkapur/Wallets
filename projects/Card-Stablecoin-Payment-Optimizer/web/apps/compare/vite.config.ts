import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

// The dev proxy injects the API key so it never reaches the browser.
const keyFile = path.resolve(__dirname, "../../../.dev/api-key");
const apiKey = process.env.OPTIMIZER_API_KEY ?? (existsSync(keyFile) ? readFileSync(keyFile, "utf8").trim() : "");
const apiTarget = process.env.OPTIMIZER_API_URL ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ""),
        headers: apiKey ? { Authorization: `Bearer ${apiKey}` } : {},
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    globals: false,
  },
});
