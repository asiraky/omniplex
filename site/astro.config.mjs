// The landing and download page, deployed to Cloudflare at omniplex.dev (see wrangler.jsonc).
import { defineConfig } from "astro/config";
import react from "@astrojs/react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  site: "https://omniplex.dev",
  trailingSlash: "ignore",
  integrations: [react()],
  build: {
    // One HTML response carries the CSS: a flaky connection pays for one round trip, not two.
    inlineStylesheets: "always",
  },
  vite: {
    plugins: [tailwindcss()],
    // Islands import motion lazily; pre-bundle it so the dev server doesn't 504 mid-optimize.
    optimizeDeps: { include: ["motion/react"] },
  },
});
