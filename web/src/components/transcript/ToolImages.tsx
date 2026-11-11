import { ImageOffIcon } from "lucide-react";
import { useRef, useState } from "react";

import { ImageViewer } from "~/components/ImageViewer";
import { cn } from "~/lib/utils";
import type { Item, ToolContent } from "~/protocol";

export interface ToolImage {
  key: string;
  url: string;
  width?: number;
  height?: number;
  /** Names the picture to a screen reader and titles the viewer. */
  label: string;
}

// Only a same-origin path is loaded. The adapter stores the bytes and names
// where they are served; anything else (a data: URL that slipped through, a
// remote host) is either the base64 this design exists to keep off the wire
// or a request to somewhere the device cookie should not be going.
function servedUrl(c: ToolContent): string | undefined {
  if (c.type !== "image" || !c.url) return undefined;
  return c.url.startsWith("/") && !c.url.startsWith("//") ? c.url : undefined;
}

/** Every image the given tool calls produced, in order. */
export function toolImages(items: Item[]): ToolImage[] {
  const out: ToolImage[] = [];
  for (const item of items) {
    if (item.kind !== "tool") continue;
    const title = item.title || "Tool result";
    const images = (item.content ?? []).flatMap((c) => {
      const url = servedUrl(c);
      return url ? [{ url, width: c.width, height: c.height }] : [];
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

// The box a thumbnail fits in. Small enough that a run of screenshots is one
// strip on a phone, big enough to tell one screen from another.
const MAX_H = 96;
const MAX_W = 192;

/**
 * Tool-result images as a strip of thumbnails, each opening the full-size
 * viewer. The strip scrolls sideways rather than wrapping, so a dozen
 * screenshots cost the transcript one row of height, not a wall.
 */
export function ToolImages({ images, className }: { images: ToolImage[]; className?: string }) {
  const [open, setOpen] = useState<number | null>(null);
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  if (images.length === 0) return null;
  const shown = open === null ? undefined : images[open];

  return (
    <div className={cn("scroll-thin flex items-end gap-1.5 overflow-x-auto overscroll-x-contain p-0.5", className)}>
      {images.map((image, i) => (
        <Thumbnail
          key={image.key}
          image={image}
          buttonRef={(el) => {
            buttons.current[i] = el;
          }}
          onOpen={() => setOpen(i)}
        />
      ))}
      {shown && open !== null && (
        <ImageViewer
          image={shown.url}
          title={shown.label}
          noun="image"
          href={shown.url}
          width={shown.width}
          height={shown.height}
          onClose={() => setOpen(null)}
          onReturnFocus={() => buttons.current[open]?.focus()}
        />
      )}
    </div>
  );
}

function Thumbnail({
  image,
  buttonRef,
  onOpen,
}: {
  image: ToolImage;
  buttonRef: (el: HTMLButtonElement | null) => void;
  onOpen: () => void;
}) {
  const [failed, setFailed] = useState(false);
  const { width: w, height: h } = image;
  // With the size known, the box is reserved before a byte arrives: a
  // screenshot landing seconds later over 4G does not shove the transcript.
  const style =
    w && h ? { width: Math.min(MAX_W, (MAX_H * w) / h, w), aspectRatio: `${w} / ${h}` } : undefined;

  return (
    <button
      ref={buttonRef}
      type="button"
      onClick={onOpen}
      aria-label={`View ${image.label} full size`}
      className="focus-visible:ring-ring shrink-0 cursor-zoom-in rounded-md outline-none focus-visible:ring-2"
    >
      {failed ? (
        <span
          style={style}
          className={cn(
            "bg-muted text-muted-foreground flex items-center justify-center rounded-md border",
            style ? "h-auto" : "size-24",
          )}
        >
          <ImageOffIcon aria-hidden className="size-4" />
        </span>
      ) : (
        <img
          src={image.url}
          alt={image.label}
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
          style={style}
          className={cn("bg-muted block rounded-md border object-cover", style ? "h-auto" : "size-24")}
        />
      )}
    </button>
  );
}
