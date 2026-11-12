// Bundles the main process and the window's preload into one CommonJS file
// each. Everything but Electron is inlined, electron-updater included, so the
// packaged app carries no node_modules and electron-builder never has to walk
// the workspace.
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";

const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const entries = { main: "src/main.ts", preload: "src/preload.ts" };

await build({
  entryPoints: Object.entries(entries).map(([out, src]) => ({ in: path.join(desktop, src), out })),
  outdir: path.join(desktop, "dist"),
  outExtension: { ".js": ".cjs" },
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"],
  sourcemap: "linked",
  logLevel: "info",
});
