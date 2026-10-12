import type { ResolvedTheme } from "./theme";

let nextId = 0;
let queue: Promise<unknown> = Promise.resolve();

// Inspect parsed attributes, not prose: labels can legitimately mention url()
// or img:. Mermaid measures image nodes and actor icons before returning SVG.
function checkResources(
  value: unknown,
  key = "",
  style = false,
  seen = new WeakSet<object>(),
) {
  if (typeof value === "string") {
    if (!style) return;
    const css = value.replace(
      /\\([\da-f]{1,6})\s?|\\([^\n])/gi,
      (_, hex: string, char: string) =>
        char ?? String.fromCodePoint(Math.min(parseInt(hex, 16), 0x10ffff)),
    );
    if (/(?:url\s*\(|@import)/i.test(css))
      throw new Error("External diagram styles are disabled");
    return;
  }
  if (!value || typeof value !== "object" || seen.has(value)) return;
  seen.add(value);
  if (value instanceof Map || value instanceof Set || Array.isArray(value)) {
    for (const child of value.values()) checkResources(child, key, style, seen);
    return;
  }
  for (const [name, child] of Object.entries(value)) {
    if (
      child &&
      (name === "img" ||
        name === "image" ||
        (key === "properties" && name === "icon"))
    ) {
      throw new Error("External diagram images are disabled");
    }
    checkResources(child, name, style || /style|css/i.test(name), seen);
  }
}

/** Mermaid has global configuration. Keep initialization and rendering together. */
export function renderMermaid(
  code: string,
  theme: ResolvedTheme,
  cancelled: () => boolean,
) {
  const job = queue.then(async () => {
    if (cancelled()) return null;
    if (code.length > 50_000) throw new Error("Diagram is too large");
    const { default: mermaid } = await import("mermaid");
    if (cancelled()) return null;
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      htmlLabels: false,
      layout: "dagre",
      theme: theme === "dark" ? "dark" : "default",
      fontFamily: "system-ui, sans-serif",
      suppressErrorRendering: true,
      // Model-supplied directives/frontmatter must not change our settings,
      // including themeCSS, fonts or diagram-specific resource options.
      secure: Object.keys(mermaid.mermaidAPI.getConfig()),
    });
    const diagram = await mermaid.mermaidAPI.getDiagramFromText(code);
    checkResources(diagram.db);
    // Diagram types expose their parsed nodes through different getters.
    for (const getter of [
      "getData",
      "getVertices",
      "getActors",
      "getClasses",
    ]) {
      const read: unknown = Reflect.get(diagram.db, getter);
      if (typeof read === "function") checkResources(read.call(diagram.db));
    }
    if (cancelled()) return null;
    const container = document.createElement("div");
    container.style.cssText =
      "position:absolute;left:-100000px;visibility:hidden";
    document.body.append(container);
    try {
      const { svg } = await mermaid.render(
        `omniplex-mermaid-${++nextId}`,
        code,
        container,
      );
      if (cancelled()) return null;
      // SVG-as-image disables scripts, links and external resources. Give it
      // intrinsic dimensions so wide diagrams scroll instead of shrinking text.
      const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
      const root = doc.documentElement;
      const viewBox = root
        .getAttribute("viewBox")
        ?.split(/[\s,]+/)
        .map(Number);
      if (viewBox?.length === 4 && viewBox.every(Number.isFinite)) {
        root.setAttribute("width", String(viewBox[2]));
        root.setAttribute("height", String(viewBox[3]));
      }
      return `data:image/svg+xml,${encodeURIComponent(new XMLSerializer().serializeToString(root))}`;
    } finally {
      container.remove();
    }
  });
  // A malformed diagram must not poison the queue for other messages.
  queue = job.catch(() => {});
  return job;
}
