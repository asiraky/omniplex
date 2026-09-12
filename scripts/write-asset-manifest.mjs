// Writes cmd/omniplex/webdist/asset-manifest.json: the list of files the
// service worker precaches, and a version that changes when any of them does.
//
// This exists because the service worker is plain JavaScript served verbatim
// (web/public/sw.js) and so cannot be told the hashed asset names at build
// time. It reads this file instead, at install and activate.
//
// The version is a hash of names and contents together, so a build that only
// renames a file still counts as new — the same rule internal/server/webassets.go
// applies to its build id, for the same reason.

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = resolve(root, "cmd/omniplex/webdist");

function walk(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...walk(full));
    else out.push(full);
  }
  return out;
}

// index.html is excluded: its URL never changes, so it cannot be precached by
// name the way a hashed asset can. The worker caches "/" for it explicitly and
// only ever serves that when the network is gone.
//
// sw.js and this manifest are excluded because a worker precaching itself is
// how one gets stuck on an old copy of itself.
const excluded = new Set(["index.html", "sw.js", "asset-manifest.json"]);

const files = walk(dist)
  .map((f) => relative(dist, f).split("\\").join("/"))
  .filter((f) => !excluded.has(f) && !f.startsWith("."))
  .sort();

const hash = createHash("sha256");
for (const file of files) {
  hash.update(file);
  hash.update(readFileSync(join(dist, file)));
}

// What the worker downloads at install time is deliberately not everything.
//
// Precaching the whole bundle would pull the terminal's chunk onto a phone that
// may never open a terminal, and would do it again on every rebuild — over the
// flaky, metered 4G this app assumes is the normal case. So install fetches the
// shell only: the entry script, its stylesheet, and the icons a home-screen
// launch paints before anything else. Every other hashed chunk is immutable and
// is cached the first time it is actually asked for.
const html = readFileSync(join(dist, "index.html"), "utf8");
const entry = [
  html.match(/<script[^>]+src="(\/assets\/[^"]+\.js)"/)?.[1],
  html.match(/<link[^>]+href="(\/assets\/[^"]+\.css)"/)?.[1],
].filter(Boolean);

// A silent miss here would produce a worker that installs fine and caches
// nothing, which only shows up as a blank page offline weeks later.
if (entry.length !== 2) {
  throw new Error("could not find the entry script and stylesheet in the built index.html");
}

const precache = [
  ...entry,
  ...files.filter((f) => !f.startsWith("assets/")).map((f) => "/" + f),
].sort();

const manifest = {
  version: hash.digest("hex").slice(0, 16),
  precache,
};

writeFileSync(join(dist, "asset-manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
console.log(
  `asset manifest: ${precache.length} of ${files.length} files precached, version ${manifest.version}`,
);
