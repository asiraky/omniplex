import type { Item } from "~/protocol";

export interface ToolImage {
  key: string;
  url: string;
  /** What the thumbnail loads: a small copy when the server made one, so a
      strip of screenshots does not pull megabytes over 4G. */
  thumbUrl: string;
  width?: number;
  height?: number;
  /** Names the picture to a screen reader and titles the viewer. */
  label: string;
}

// Only a same-origin path is loaded. The adapter stores the bytes and names
// where they are served; anything else (a data: URL that slipped through, a
// remote host) is either the base64 this design exists to keep off the wire
// or a request to somewhere the device cookie should not be going.
function served(url: string | undefined): string | undefined {
  return url?.startsWith("/") && !url.startsWith("//") ? url : undefined;
}

/** Every image the given tool calls produced, in order. */
export function toolImages(items: Item[]): ToolImage[] {
  const out: ToolImage[] = [];
  for (const item of items) {
    if (item.kind !== "tool") continue;
    const title = item.title || "Tool result";
    const images = (item.content ?? []).flatMap((c) => {
      const url = c.type === "image" ? served(c.url) : undefined;
      if (!url) return [];
      return [{ url, thumbUrl: served(c.thumbUrl) ?? url, width: c.width, height: c.height }];
    });
    images.forEach((image, i) =>
      out.push({
        ...image,
        key: `${item.id}:${i}`,
        label: images.length > 1 ? `${title} (image ${i + 1} of ${images.length})` : `${title} image`,
      }),
    );
  }
  return out;
}
