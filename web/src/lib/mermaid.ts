import type { ResolvedTheme } from "./theme";

let nextId = 0;
let queue: Promise<unknown> = Promise.resolve();

/** Mermaid has global configuration. Keep initialization and rendering together. */
export function renderMermaid(
  code: string,
  theme: ResolvedTheme,
  cancelled: () => boolean,
) {
  const job = queue.then(async () => {
    if (cancelled()) return null;
    // Image nodes and CSS resources would fetch while Mermaid measures its
    // temporary DOM, before the result is isolated in an <img>.
    // Decode CSS/YAML escapes for the resource check as well as quoted keys.
    const unescaped = code.replace(
      /\\(?:u([\da-f]{4})|x([\da-f]{2})|([\da-f]{1,6})\s?|([^\n]))/gi,
      (_, unicode: string, hex: string, css: string, char: string) =>
        char ??
        String.fromCodePoint(
          Math.min(parseInt(unicode ?? hex ?? css, 16), 0x10ffff),
        ),
    );
    if (
      code.length > 50_000 ||
      /(?:\bimg["']?\s*:|url\s*\(|@import)/i.test(unescaped)
    ) {
      throw new Error("Diagram contains external resources or is too large");
    }
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
