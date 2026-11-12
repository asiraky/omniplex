import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

// Ports are shared with the Go server through the environment so the two
// cannot disagree; the root development script sets them.
const vitePort = Number(process.env.OMNIPLEX_VITE_PORT ?? 5199);
const serverPort = Number(process.env.OMNIPLEX_PORT ?? 8787);

// The Go server fronts the app in development as well as in production: it
// proxies anything that is not the API through to this dev server. So there is
// one URL and one origin either way, and this server only ever needs to be
// reachable from the Go process on loopback — never from the network.
export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      name: "preserve-webdist-placeholder",
      closeBundle: () => writeFile(new URL("../cmd/omniplex/webdist/.gitkeep", import.meta.url), ""),
    },
  ],
  resolve: {
    // Mirrors the `~/*` path mapping in tsconfig.json; shadcn components
    // are generated with this alias, so the two must agree.
    alias: {
      "~": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: vitePort,

    // The proxy target is a fixed port. Letting Vite drift to the next free
    // one when this is taken would leave the Go server proxying into nothing,
    // which is a confusing failure — better to say so at startup.
    strictPort: true,

    // Loopback only. The browser reaches Vite through the Go server, so
    // exposing this port to the network would add a second, unauthenticated
    // way in.
    host: "127.0.0.1",

    hmr: {
      // The page is served from the Go port, so the HMR socket has to go
      // there too and be proxied through. Pointing it at Vite's own port
      // would send the browser somewhere it cannot reach from a phone.
      clientPort: serverPort,
    },
  },
  build: {
    outDir: "../cmd/omniplex/webdist",
    emptyOutDir: true,
  },
});
