// Downloads and the copy button for the bento board.
// Plain DOM on top of lib/download.js. The page reads fine if this never runs.
import {
  allDownloads,
  detectPlatform,
  INSTALL_COMMAND,
  LATEST_API_URL,
  primaryDownload,
  RELEASES_URL,
} from "../lib/download.js";

type Release = {
  tag_name: string;
  html_url: string;
  assets: { name: string; browser_download_url: string }[];
};
type Choice = ReturnType<typeof primaryDownload>;

const API_URL = import.meta.env.PUBLIC_LATEST_API_URL || LATEST_API_URL;
const all = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document) => [
  ...root.querySelectorAll<T>(sel),
];

async function readPlatform() {
  const ua = (
    navigator as Navigator & {
      userAgentData?: {
        platform?: string;
        getHighEntropyValues?: (hints: string[]) => Promise<{ architecture?: string }>;
      };
    }
  ).userAgentData;
  let architecture = "";
  try {
    if (ua?.getHighEntropyValues) ({ architecture = "" } = await ua.getHighEntropyValues(["architecture"]));
  } catch {
    // Refused or unsupported: detectPlatform's defaults apply.
  }
  return detectPlatform({
    userAgent: navigator.userAgent,
    platform: ua?.platform || navigator.platform || "",
    maxTouchPoints: navigator.maxTouchPoints || 0,
    architecture,
  });
}

// Same cache key as the original page: 60 unauthenticated API calls an hour per IP.
const CACHE_KEY = "omniplex-latest-release";
async function fetchRelease(): Promise<Release | null> {
  try {
    const cached = JSON.parse(sessionStorage.getItem(CACHE_KEY) || "null");
    if (cached && Date.now() - cached.at < 10 * 60 * 1000) return cached.release;
  } catch {}
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), 10000);
  try {
    const res = await fetch(API_URL, { signal: abort.signal, headers: { Accept: "application/vnd.github+json" } });
    if (!res.ok) return null;
    const json = await res.json();
    const release: Release = {
      tag_name: json.tag_name,
      html_url: json.html_url,
      assets: (json.assets || []).map((a: Release["assets"][number]) => ({
        name: a.name,
        browser_download_url: a.browser_download_url,
      })),
    };
    try {
      sessionStorage.setItem(CACHE_KEY, JSON.stringify({ at: Date.now(), release }));
    } catch {}
    return release;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

const NOTES: Record<string, string> = {
  "coming-soon": "The desktop app is macOS only for now. Watch the releases page for the Windows build.",
  terminal: "There is no desktop app for Linux. The server runs from a terminal.",
  "other-device":
    "Open this page on the computer you want to run Omniplex on. Once it is running, you can use it from this device.",
};

function renderPrimary(choice: Choice) {
  for (const root of all("[data-dl]")) {
    root.dataset.state = choice.kind;
    for (const el of all("[data-when]", root)) el.hidden = !el.dataset.when!.split(" ").includes(choice.kind);
    for (const el of all("[data-dl-status]", root)) el.textContent = choice.title;
    for (const el of all("[data-dl-note]", root)) el.textContent = NOTES[choice.kind] ?? "";
    const alt = choice.kind === "download" ? choice.alternate : null;
    for (const el of all("[data-dl-alt]", root)) el.hidden = !alt;
    if (choice.kind !== "download") continue;
    for (const a of all<HTMLAnchorElement>("[data-dl-primary]", root)) a.href = choice.url;
    for (const el of all("[data-dl-title]", root)) el.textContent = choice.title;
    for (const el of all("[data-dl-detail]", root)) el.textContent = choice.detail || "From the GitHub releases page";
    if (alt) {
      for (const el of all("[data-dl-alt-prompt]", root)) el.textContent = alt.prompt;
      for (const a of all<HTMLAnchorElement>("[data-dl-alt-link]", root)) a.href = alt.url;
    }
  }
}

// One plain row per file in the latest release.
function renderList(release: Release | null) {
  const body = document.querySelector<HTMLElement>("[data-dl-list]");
  if (!body) return;
  const rows = allDownloads(release);
  body.replaceChildren();
  if (rows.length === 0) {
    const li = document.createElement("li");
    li.className = "file-empty";
    const a = document.createElement("a");
    a.href = release?.html_url || RELEASES_URL;
    a.textContent = "the releases page";
    li.append(
      release
        ? "This release has no downloads yet. See "
        : "No release to list yet, or GitHub did not answer. Files are listed here once a release is out. Until then, see ",
      a,
      ".",
    );
    body.append(li);
    return;
  }
  for (const row of rows) {
    const li = document.createElement("li");
    const a = document.createElement("a");
    a.className = "file";
    a.href = row.url;
    const label = document.createElement("span");
    label.className = "file-label";
    label.textContent = row.label;
    const file = document.createElement("span");
    file.className = "file-name";
    file.textContent = row.file;
    a.append(label, file);
    li.append(a);
    body.append(li);
  }
}

for (const el of all("[data-install-cmd]")) el.textContent = INSTALL_COMMAND;
for (const button of all<HTMLButtonElement>("[data-copy]")) {
  const label = button.querySelector<HTMLElement>("[data-copy-label]") ?? button;
  const idle = label.textContent;
  button.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(INSTALL_COMMAND);
      label.textContent = "Copied";
      setTimeout(() => (label.textContent = idle), 1500);
    } catch {
      const cmd = button.closest("[data-copy-scope]")?.querySelector("[data-install-cmd]");
      if (cmd) getSelection()?.selectAllChildren(cmd);
    }
  });
}

const platform = await readPlatform();
renderPrimary(primaryDownload(null, platform));
const release = await fetchRelease();
renderPrimary(primaryDownload(release, platform));
renderList(release);
const version = document.querySelector<HTMLElement>("[data-dl-version]");
if (version && release?.tag_name) {
  version.textContent = release.tag_name;
  version.hidden = false;
}
