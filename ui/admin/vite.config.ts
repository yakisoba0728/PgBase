import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  // font-src and img-src forbid inlined data: fonts, so every asset is emitted as a file.
  build: { assetsInlineLimit: 0 },
  server: {
    proxy: {
      "/api": "http://localhost:8090",
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: "./src/test/setup.ts",
    globals: true,
  },
});
