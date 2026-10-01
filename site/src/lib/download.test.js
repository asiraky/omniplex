import { test } from "node:test";
import assert from "node:assert/strict";
import { allDownloads, classifyAsset, detectPlatform, primaryDownload, RELEASES_URL } from "./download.js";

const asset = (name) => ({ name, browser_download_url: `https://dl.example/${name}` });
const release = (...names) => ({
  tag_name: "v1.2.0",
  html_url: "https://github.com/asiraky/omniplex/releases/tag/v1.2.0",
  assets: names.map(asset),
});

// What electron-builder (artifactName "${productName}-${version}-${os}-${arch}.${ext}")
// and the server job publish for a full release.
const full = release(
  "Omniplex-1.2.0-mac-arm64.dmg",
  "Omniplex-1.2.0-mac-arm64.dmg.blockmap",
  "Omniplex-1.2.0-mac-x64.dmg",
  "Omniplex-1.2.0-mac-arm64.zip",
  "Omniplex-1.2.0-mac-x64.zip",
  "latest-mac.yml",
  "Omniplex-1.2.0-win-x64.exe",
  "Omniplex-1.2.0-win-x64.exe.blockmap",
  "latest.yml",
  "omniplex-1.2.0-darwin-arm64.tar.gz",
  "omniplex-1.2.0-darwin-amd64.tar.gz",
  "omniplex-1.2.0-linux-amd64.tar.gz",
  "omniplex-1.2.0-linux-arm64.tar.gz",
  "SHA256SUMS",
);
const macOnly = release("Omniplex-1.2.0-mac-arm64.dmg", "Omniplex-1.2.0-mac-x64.dmg", "omniplex-1.2.0-linux-amd64.tar.gz");

const SAFARI_MAC = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15";
const CHROME_WIN = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36";

test("a Mac without an architecture hint is taken to be Apple Silicon", () => {
  assert.deepEqual(detectPlatform({ userAgent: SAFARI_MAC, platform: "MacIntel" }), { os: "mac", arch: "arm64" });
});

test("the client hint decides the Mac's chip", () => {
  const intel = detectPlatform({ userAgent: SAFARI_MAC, platform: "MacIntel", architecture: "x86" });
  const arm = detectPlatform({ userAgent: SAFARI_MAC, platform: "MacIntel", architecture: "arm" });
  assert.equal(intel.arch, "x64");
  assert.equal(arm.arch, "arm64");
});

test("an iPad asking for the desktop site is not a Mac", () => {
  assert.equal(detectPlatform({ userAgent: SAFARI_MAC, platform: "MacIntel", maxTouchPoints: 5 }).os, "ios");
});

test("Windows is x64 unless the hint says ARM", () => {
  assert.deepEqual(detectPlatform({ userAgent: CHROME_WIN, platform: "Win32" }), { os: "windows", arch: "x64" });
  assert.equal(detectPlatform({ userAgent: CHROME_WIN, platform: "Win32", architecture: "arm" }).arch, "arm64");
});

test("phones and Linux are recognised", () => {
  assert.equal(detectPlatform({ userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)" }).os, "ios");
  assert.equal(detectPlatform({ userAgent: "Mozilla/5.0 (Linux; Android 15; Pixel 9)" }).os, "android");
  assert.deepEqual(detectPlatform({ userAgent: "Mozilla/5.0 (X11; Linux aarch64)", platform: "Linux aarch64" }), {
    os: "linux",
    arch: "arm64",
  });
});

test("updater files and checksums are not downloads", () => {
  for (const name of ["latest-mac.yml", "latest.yml", "Omniplex-1.2.0-mac-arm64.dmg.blockmap", "Omniplex-1.2.0-mac-arm64.zip", "SHA256SUMS"]) {
    assert.equal(classifyAsset(name), null, name);
  }
});

test("a dmg with no arch in its name is x64, electron-builder's default", () => {
  assert.equal(classifyAsset("Omniplex-1.2.0.dmg").arch, "x64");
  assert.equal(classifyAsset("Omniplex-1.2.0-universal.dmg").arch, "universal");
});

test("server tarballs map Go's names to the page's", () => {
  assert.deepEqual(classifyAsset("omniplex-1.2.0-darwin-amd64.tar.gz"), { kind: "server", os: "mac", arch: "x64" });
  assert.deepEqual(classifyAsset("omniplex-1.2.0-rc.1-linux-arm64.tar.gz"), { kind: "server", os: "linux", arch: "arm64" });
});

test("each Mac gets its own chip's dmg, with the other one offered beside it", () => {
  const arm = primaryDownload(full, { os: "mac", arch: "arm64" });
  assert.equal(arm.kind, "download");
  assert.equal(arm.url, "https://dl.example/Omniplex-1.2.0-mac-arm64.dmg");
  assert.equal(arm.alternate.url, "https://dl.example/Omniplex-1.2.0-mac-x64.dmg");

  const intel = primaryDownload(full, { os: "mac", arch: "x64" });
  assert.equal(intel.url, "https://dl.example/Omniplex-1.2.0-mac-x64.dmg");
  assert.equal(intel.alternate.url, "https://dl.example/Omniplex-1.2.0-mac-arm64.dmg");
});

test("a universal dmg serves both chips and needs no alternate", () => {
  const r = release("Omniplex-1.2.0-universal.dmg");
  for (const arch of ["arm64", "x64"]) {
    const d = primaryDownload(r, { os: "mac", arch });
    assert.equal(d.url, "https://dl.example/Omniplex-1.2.0-universal.dmg");
    assert.equal(d.alternate, null);
  }
});

test("Windows gets the installer", () => {
  const d = primaryDownload(full, { os: "windows", arch: "x64" });
  assert.equal(d.kind, "download");
  assert.equal(d.url, "https://dl.example/Omniplex-1.2.0-win-x64.exe");
});

test("Windows on ARM falls back to the x64 installer", () => {
  assert.equal(primaryDownload(full, { os: "windows", arch: "arm64" }).url, "https://dl.example/Omniplex-1.2.0-win-x64.exe");
});

test("a release with no Windows installer says Windows is coming, not a dead link", () => {
  assert.equal(primaryDownload(macOnly, { os: "windows", arch: "x64" }).kind, "coming-soon");
});

test("an Intel Mac with only an Apple Silicon build is sent to the release page", () => {
  const d = primaryDownload(release("Omniplex-1.2.0-mac-arm64.dmg"), { os: "mac", arch: "x64" });
  assert.equal(d.kind, "download");
  assert.equal(d.url, "https://github.com/asiraky/omniplex/releases/tag/v1.2.0");
});

test("without the API every button goes to the releases page", () => {
  const mac = primaryDownload(null, { os: "mac", arch: "arm64" });
  assert.equal(mac.url, RELEASES_URL);
  assert.equal(mac.alternate.url, RELEASES_URL);
  assert.equal(primaryDownload(null, { os: "windows", arch: "x64" }).url, RELEASES_URL);
});

test("Linux is pointed at the terminal install, phones at a computer", () => {
  assert.equal(primaryDownload(full, { os: "linux", arch: "x64" }).kind, "terminal");
  assert.equal(primaryDownload(full, { os: "ios", arch: null }).kind, "other-device");
  assert.equal(primaryDownload(full, { os: "unknown", arch: null }).kind, "other-device");
});

test("the full list puts installers before server tarballs and drops updater files", () => {
  const rows = allDownloads(full);
  assert.deepEqual(
    rows.map((r) => r.file),
    [
      "Omniplex-1.2.0-mac-arm64.dmg",
      "Omniplex-1.2.0-mac-x64.dmg",
      "Omniplex-1.2.0-win-x64.exe",
      "omniplex-1.2.0-darwin-arm64.tar.gz",
      "omniplex-1.2.0-darwin-amd64.tar.gz",
      "omniplex-1.2.0-linux-arm64.tar.gz",
      "omniplex-1.2.0-linux-amd64.tar.gz",
    ],
  );
  assert.deepEqual(allDownloads(null), []);
});
