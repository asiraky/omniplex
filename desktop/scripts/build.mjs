// Bundles the main process into one CommonJS file. Everything but Electron is
// inlined, electron-updater included, so the packaged app carries no
// node_modules and electron-builder never has to walk the workspace.
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";

const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

await build({
  entryPoints: [path.join(desktop, "src/main.ts")],
  outfile: path.join(desktop, "dist/main.cjs"),
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"],
  sourcemap: "linked",
  logLevel: "info",
});
