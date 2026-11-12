import { ImageOffIcon } from "lucide-react";
import { lazy, Suspense, useEffect, useRef, useState, type ComponentProps, type ComponentType } from "react";

import type { ToolImage } from "~/lib/toolImages";
import { cn } from "~/lib/utils";

// Opens on a tap, never on first paint, so it stays out of the initial bundle.
// With no error boundary above it, a chunk that fails to load on a bad link or
// after a redeploy would take the app down, so the tap offers the original
// instead.
type Viewer = ComponentType<ComponentProps<typeof import("~/components/ImageViewer").ImageViewer>>;
const ViewerUnavailable: Viewer = ({ image, href }) => (
  <p role="alert" className="text-destructive shrink-0 self-center text-[12px]">
    Could not open the viewer.{" "}
    <a href={href ?? image} target="_blank" rel="noreferrer" className="underline">
      Open the image
    </a>
  </p>
);
// The mount prefetch and the lazy render share one load.
let viewer: Promise<{ default: Viewer }> | undefined;
const loadViewer = () =>
  (viewer ??= import("~/components/ImageViewer").then(
    (m): { default: Viewer } => ({ default: m.ImageViewer }),
    () => ({ default: ViewerUnavailable }),
  ));
const ImageViewer = lazy(loadViewer);

// The box a thumbnail fits in. Small enough that a run of screenshots is one
// strip on a phone, big enough to tell one screen from another.
const MAX_H = 96;
const MAX_W = 192;
// How far the box may stray from square. A full-page screenshot or a
// panorama past these is cropped to its top or left, not drawn as a sliver.
const TALLEST = 1 / 2;
const WIDEST = MAX_W / (MAX_H / 2);

/**
 * Tool-result images as a strip of thumbnails, each opening the full-size
 * viewer. The strip scrolls sideways rather than wrapping, so a dozen
 * screenshots cost the transcript one row of height, not a wall.
 */
export function ToolImages({ images, className }: { images: ToolImage[]; className?: string }) {
  const [open, setOpen] = useState<number | null>(null);
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  const hasImages = images.length > 0;
  // Fetched once there is a thumbnail to tap, after first paint, so the tap
  // opens the viewer at once rather than a 4G round trip later.
  useEffect(() => {
    if (hasImages) void loadViewer();
  }, [hasImages]);
  if (!hasImages) return null;
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

/** A w×h image's thumbnail box: within MAX_W×MAX_H, never upscaled. */
function box(w: number, h: number) {
  const ratio = Math.min(WIDEST, Math.max(TALLEST, w / h));
  return {
    width: Math.min(MAX_W, MAX_H * ratio, w),
    aspectRatio: ratio === w / h ? `${w} / ${h}` : `${ratio} / 1`,
  };
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
  const style = w && h ? box(w, h) : undefined;

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
          className={cn("bg-muted block rounded-md border object-cover object-left-top", style ? "h-auto" : "size-24")}
        />
      )}
    </button>
  );
}
