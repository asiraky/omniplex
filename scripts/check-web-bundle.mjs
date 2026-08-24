import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = resolve(root, "cmd/omniplex/webdist");
const html = readFileSync(resolve(dist, "index.html"), "utf8");
const entryJS = html.match(/<script[^>]+src="([^"]+\.js)"/)?.[1];
const entryCSS = html.match(/<link[^>]+href="([^"]+\.css)"/)?.[1];

if (!entryJS || !entryCSS) throw new Error("could not find initial JS and CSS in built index.html");

const gzipKB = (asset) => gzipSync(readFileSync(resolve(dist, asset.replace(/^\//, "")))).byteLength / 1024;
const jsKB = gzipKB(entryJS);
const cssKB = gzipKB(entryCSS);
const limits = { js: 250, css: 20 };

console.log(`initial bundle: JS ${jsKB.toFixed(1)} KiB gzip, CSS ${cssKB.toFixed(1)} KiB gzip`);
if (jsKB > limits.js || cssKB > limits.css) {
  throw new Error(`initial bundle exceeds budget (JS ${limits.js} KiB, CSS ${limits.css} KiB gzip)`);
}
