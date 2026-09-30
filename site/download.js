// What to offer a visitor, given their machine and the latest GitHub release.
// No DOM here: index.html does the rendering, download.test.js the checking.

export const REPO = "asiraky/omniplex";
export const RELEASES_URL = `https://github.com/${REPO}/releases/latest`;
export const LATEST_API_URL = `https://api.github.com/repos/${REPO}/releases/latest`;
export const INSTALL_COMMAND = `curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | sh`;

const OS_LABEL = { mac: "macOS", windows: "Windows", linux: "Linux" };
const ARCH_LABEL = {
  mac: { arm64: "Apple Silicon", x64: "Intel" },
  windows: { arm64: "ARM", x64: "x64" },
  linux: { arm64: "arm64", x64: "x64" },
};

/**
 * Work out the visitor's OS and CPU architecture.
 *
 * `architecture` is Chromium's high-entropy client hint ("arm" or "x86"), when
 * the browser gives one. Every Mac browser claims "Intel Mac OS X" in its user
 * agent whatever the chip, so without the hint a Mac is assumed to be Apple
 * Silicon, which is what anyone buying a Mac since 2020 has.
 */
export function detectPlatform({
  userAgent = "",
  platform = "",
  maxTouchPoints = 0,
  architecture = "",
} = {}) {
  const ua = userAgent.toLowerCase();
  const plat = platform.toLowerCase();
  const hintArch = architecture === "arm" ? "arm64" : architecture === "x86" ? "x64" : null;

  if (/iphone|ipod/.test(ua)) return { os: "ios", arch: null };
  if (/android/.test(ua)) return { os: "android", arch: null };
  // iPadOS asks for desktop sites and says "Macintosh"; only the touch screen gives it away.
  if (/ipad/.test(ua) || (/macintosh/.test(ua) && maxTouchPoints > 1)) return { os: "ios", arch: null };

  if (/mac/.test(plat) || /macintosh|mac os x/.test(ua)) {
    return { os: "mac", arch: hintArch ?? "arm64" };
  }
  if (/win/.test(plat) || /windows/.test(ua)) {
    const arm = hintArch === "arm64" || /\barm64\b|\baarch64\b/.test(ua);
    return { os: "windows", arch: arm ? "arm64" : "x64" };
  }
  if (/linux|x11|cros/.test(plat + " " + ua)) {
    const arm = hintArch === "arm64" || /aarch64|arm64|armv8/.test(ua);
    return { os: "linux", arch: arm ? "arm64" : "x64" };
  }
  return { os: "unknown", arch: null };
}

/**
 * Say what a release asset is, or null when it is nothing a person downloads
 * by hand (updater feeds, blockmaps, the updater's zip, checksums).
 *
 * Desktop installers are matched by extension and an arch word anywhere in
 * the name, so this survives electron-builder's artifactName changing. A mac
 * dmg with no arch in its name is x64: that is electron-builder's default.
 */
export function classifyAsset(name) {
  const lower = name.toLowerCase();

  const server = /^omniplex-(.+)-(darwin|linux)-(amd64|arm64)\.tar\.gz$/.exec(lower);
  if (server) {
    return {
      kind: "server",
      os: server[2] === "darwin" ? "mac" : "linux",
      arch: server[3] === "amd64" ? "x64" : "arm64",
    };
  }

  if (lower.endsWith(".dmg")) {
    let arch = "x64";
    if (/universal/.test(lower)) arch = "universal";
    else if (/arm64|aarch64/.test(lower)) arch = "arm64";
    return { kind: "desktop", os: "mac", arch };
  }

  if (lower.endsWith(".exe")) {
    return { kind: "desktop", os: "windows", arch: /arm64|aarch64/.test(lower) ? "arm64" : "x64" };
  }

  return null;
}

/** The desktop installer for os/arch, falling back to a universal mac build. */
export function findInstaller(assets, os, arch) {
  let universal = null;
  for (const asset of assets ?? []) {
    const info = classifyAsset(asset.name);
    if (!info || info.kind !== "desktop" || info.os !== os) continue;
    if (info.arch === arch) return asset;
    if (info.arch === "universal") universal = asset;
  }
  return universal;
}

function label(os, arch) {
  return `${OS_LABEL[os]} (${ARCH_LABEL[os][arch]})`;
}

/**
 * The main call to action for this visitor.
 *
 * `release` is the GitHub API's latest-release object, or null when the API
 * could not be reached or rate-limited us; then every link goes to the
 * releases page rather than guessing a file name.
 *
 * Returns one of:
 *   { kind: "download", os, arch, title, detail, url, alternate? }
 *   { kind: "coming-soon", os, title }       no build for this OS yet
 *   { kind: "terminal", os, title }          Linux: the server only, from a terminal
 *   { kind: "other-device", title }          phone or tablet, or an OS we cannot tell
 */
export function primaryDownload(release, { os, arch }) {
  if (os === "linux") {
    return { kind: "terminal", os, title: "Install on Linux" };
  }
  if (os !== "mac" && os !== "windows") {
    return { kind: "other-device", title: "Omniplex installs on a Mac or Windows computer" };
  }

  const title = `Download for ${OS_LABEL[os]}`;
  const version = release?.tag_name ?? null;

  if (!release) {
    return {
      kind: "download",
      os,
      arch,
      title,
      detail: os === "mac" ? ARCH_LABEL.mac[arch] : null,
      url: RELEASES_URL,
      alternate: os === "mac" ? macAlternate(null, arch) : undefined,
    };
  }

  let asset = findInstaller(release.assets, os, arch);
  // A Windows ARM machine runs x64 installers under emulation; better than nothing.
  if (!asset && os === "windows" && arch === "arm64") asset = findInstaller(release.assets, os, "x64");

  if (!asset) {
    const anyForOs = (release.assets ?? []).some((a) => {
      const info = classifyAsset(a.name);
      return info?.kind === "desktop" && info.os === os;
    });
    if (!anyForOs) return { kind: "coming-soon", os, title: `${OS_LABEL[os]} is coming soon` };
    return { kind: "download", os, arch, title, detail: version, url: release.html_url ?? RELEASES_URL };
  }

  const detail = [os === "mac" ? ARCH_LABEL.mac[arch] : null, version].filter(Boolean).join(" · ");
  return {
    kind: "download",
    os,
    arch,
    title,
    detail,
    url: asset.browser_download_url,
    alternate: os === "mac" ? macAlternate(release, arch) : undefined,
  };
}

// The "Intel Mac?" / "Apple Silicon Mac?" link under the mac button, since
// the arch guess is a guess.
function macAlternate(release, arch) {
  const other = arch === "arm64" ? "x64" : "arm64";
  const prompt = other === "x64" ? "Intel Mac?" : "Apple Silicon Mac?";
  if (!release) return { prompt, url: RELEASES_URL };
  const asset = findInstaller(release.assets, "mac", other);
  if (!asset || classifyAsset(asset.name).arch === "universal") return null;
  return { prompt, url: asset.browser_download_url };
}

/**
 * Every download in the release, for the "All downloads" list: installers
 * first, then the server tarballs for terminal installs.
 */
export function allDownloads(release) {
  const rows = [];
  for (const asset of release?.assets ?? []) {
    const info = classifyAsset(asset.name);
    if (!info) continue;
    const name =
      info.arch === "universal"
        ? `${OS_LABEL[info.os]} (Universal)`
        : label(info.os, info.arch);
    rows.push({
      kind: info.kind,
      os: info.os,
      arch: info.arch,
      label: info.kind === "server" ? `Server only, ${name}` : name,
      file: asset.name,
      url: asset.browser_download_url,
    });
  }
  const order = (r) =>
    (r.kind === "desktop" ? 0 : 10) + (r.os === "mac" ? 0 : r.os === "windows" ? 3 : 6) + (r.arch === "x64" ? 1 : 0);
  return rows.sort((a, b) => order(a) - order(b));
}
