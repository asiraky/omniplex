import { ImageOffIcon } from "lucide-react";
import { lazy, Suspense, useRef, useState } from "react";

import type { ToolImage } from "~/lib/toolImages";
import { cn } from "~/lib/utils";

// Opens on a tap, never on first paint, so it stays out of the initial bundle.
const ImageViewer = lazy(() => import("~/components/ImageViewer").then((m) => ({ default: m.ImageViewer })));

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
        <Suspense fallback={null}>
          <ImageViewer
            image={shown.url}
            title={shown.label}
            href={shown.url}
            width={shown.width}
            height={shown.height}
            onClose={() => setOpen(null)}
            onReturnFocus={() => buttons.current[open]?.focus()}
          />
        </Suspense>
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
          src={image.thumbUrl}
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
