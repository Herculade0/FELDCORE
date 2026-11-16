import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// During `npm run dev`, API + WebSocket calls are proxied to the running
// feldcored daemon so the front-end and back-end can be developed separately.
export default defineConfig({
  base: "./",
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8787",
        // Keep the browser's Host header: the daemon checks that Origin
        // matches Host on writes and on the WebSocket handshake.
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
