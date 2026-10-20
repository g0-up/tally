import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { VitePWA } from "vite-plugin-pwa";
import { BRAND_NAME, BRAND_TAGLINE } from "./src/app/layouts/brand.ts";

const apiTarget = process.env.VITE_API_PROXY ?? "http://localhost:8080";

// Same origin as the API in dev, like production behind Traefik: no CORS, cookies behave the same.
// changeOrigin stays false so the API sees the browser's Host (tenant slug in <slug>.localhost:5173).
const apiProxy = { "/api": { target: apiTarget, changeOrigin: false, xfwd: true } };

// Installable shell for the POS. The service worker is our own (src/sw.ts): it precaches the
// hashed bundle and never caches API answers, which plain generateSW would. One build serves
// every store host, so start_url and scope are relative and the name is the app's, not a store's.
// main.tsx registers it on store hosts only.
const pwa = VitePWA({
  strategies: "injectManifest",
  srcDir: "src",
  filename: "sw.ts",
  registerType: "prompt",
  injectRegister: false,
  manifest: {
    name: BRAND_NAME,
    short_name: BRAND_NAME,
    description: BRAND_TAGLINE,
    lang: "vi",
    start_url: "/sales/pos",
    scope: "/",
    display: "standalone",
    background_color: "#ffffff",
    theme_color: "#233c65",
    icons: [
      { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png" },
      { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png" },
      { src: "/icons/maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
      { src: "/icons/apple-touch-icon.png", sizes: "180x180", type: "image/png" },
    ],
  },
  // The barcode reader (and its .wasm, which the patterns never match) is fetched by the phone scan
  // page alone, so no store device downloads it with the precache.
  injectManifest: { globPatterns: ["**/*.{js,css,html,png,svg,woff2}"], globIgnores: ["**/barcode-reader-*.js"] },
  // Unit tests never load the service worker.
  disable: Boolean(process.env.VITEST),
});

export default defineConfig({
  plugins: [react(), tailwindcss(), pwa],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "src") },
  },
  build: {
    rollupOptions: {
      output: {
        // The ZXing ponyfill and its loader in one lazy chunk with a stable name, kept out of the
        // precache by globIgnores above.
        manualChunks: (id) => (/[\\/]node_modules[\\/].*(barcode-detector|zxing-wasm)[\\/]/.test(id) ? "barcode-reader" : undefined),
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    // Tenant hosts in dev are http://<slug>.localhost:5173; *.localhost resolves to loopback without DNS.
    allowedHosts: [".localhost"],
    proxy: apiProxy,
  },
  preview: {
    port: 4173,
    strictPort: true,
    allowedHosts: [".localhost"],
    proxy: apiProxy,
  },
});
