import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "https://argentum-api.gaia.smartsoft.co.id",
        changeOrigin: true,
        secure: true,
        ws: true,
      },
      "/webhook": {
        target: "https://argentum-api.gaia.smartsoft.co.id",
        changeOrigin: true,
        secure: true,
      },
      "/metabase": {
        target: "https://argentum-api.gaia.smartsoft.co.id",
        changeOrigin: true,
        secure: true,
      },
    },
  },
});
