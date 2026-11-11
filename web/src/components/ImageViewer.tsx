import { ExternalLinkIcon, MinusIcon, PlusIcon, XIcon } from "lucide-react";
import { Dialog } from "radix-ui";
import { useCallback, useLayoutEffect, useRef, useState } from "react";

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 8;
const control =
  "hover:bg-accent focus-visible:ring-ring flex size-11 shrink-0 items-center justify-center rounded-md outline-none focus-visible:ring-2 disabled:opacity-40";

/**
 * One picture, full screen: fitted to the viewport first, then zoomable by
 * button or key, with a one-press jump to its own pixel size for a screenshot
 * too big to read fitted. A known `width`/`height` sizes the frame before the
 * bytes arrive, so a slow load does not reflow the dialog when they do.
 * `href` adds a link that opens the original in a new tab.
 */
export function ImageViewer({
  image,
  title = "Image",
  alt = title,
  noun = "image",
  href,
  width: knownWidth,
  height: knownHeight,
  onClose,
  onReturnFocus,
}: {
  image: string;
  title?: string;
  alt?: string;
  /** What the controls call the picture: "Fit diagram", "Image zoom". */
  noun?: string;
  href?: string;
  width?: number;
  height?: number;
  onClose: () => void;
  onReturnFocus: () => void;
}) {
  const [zoom, setZoom] = useState(1);
  const [size, setSize] = useState({ width: knownWidth || 0, height: knownHeight || 0 });
  const [bounds, setBounds] = useState({ width: 0, height: 0 });
  const scroller = useRef<HTMLDivElement>(null);
  const previous = useRef(1);
  const mountScroller = useCallback((el: HTMLDivElement | null) => {
    scroller.current = el;
    if (!el) return;
    const observer = new ResizeObserver(([entry]) => {
      setBounds({
        width: entry.contentRect.width,
        height: entry.contentRect.height,
      });
    });
    observer.observe(el);
    return () => {
      observer.disconnect();
      scroller.current = null;
    };
  }, []);
  const fit =
    size.width && bounds.width
      ? Math.max(
          0.01,
          Math.min(
            (bounds.width - 32) / size.width,
            (bounds.height - 32) / size.height,
            1,
          ),
        )
      : 1;
  // Zoom is relative to the fitted size, so its own pixel size is 1/fit.
  const actual = Math.min(MAX_ZOOM, 1 / fit);
  const width = size.width * fit * zoom;
  const height = size.height * fit * zoom;

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) {
      const ratio = zoom / previous.current;
      el.scrollLeft =
        (el.scrollLeft + el.clientWidth / 2) * ratio - el.clientWidth / 2;
      el.scrollTop =
        (el.scrollTop + el.clientHeight / 2) * ratio - el.clientHeight / 2;
    }
    previous.current = zoom;
  }, [zoom]);

  function changeZoom(direction: number) {
    setZoom((value) =>
      Math.min(
        MAX_ZOOM,
        Math.max(MIN_ZOOM, value * (direction > 0 ? 1.25 : 0.8)),
      ),
    );
  }

  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[100] bg-background" />
        <Dialog.Content
          aria-describedby={undefined}
          className="bg-background fixed inset-0 z-[100] flex h-dvh w-full flex-col overflow-hidden outline-none"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            onReturnFocus();
          }}
          onKeyDown={(event) => {
            if (event.key === "+" || event.key === "=") {
              event.preventDefault();
              changeZoom(1);
            } else if (event.key === "-") {
              event.preventDefault();
              changeZoom(-1);
            } else if (event.key === "0") {
              event.preventDefault();
              setZoom(1);
            } else if (event.key === "1") {
              event.preventDefault();
              setZoom(actual);
            }
          }}
        >
          <div className="flex shrink-0 items-center gap-2 border-b pr-[max(0.5rem,env(safe-area-inset-right))] pl-[max(0.75rem,env(safe-area-inset-left))] pt-[env(safe-area-inset-top)]">
            <Dialog.Title className="min-w-0 flex-1 truncate text-sm font-medium">
              {title}
            </Dialog.Title>
            <div className="flex shrink-0 items-center gap-1">
              <button
                type="button"
                aria-label="Zoom out"
                title="Zoom out (-)"
                disabled={zoom <= MIN_ZOOM}
                onClick={() => changeZoom(-1)}
                className={control}
              >
                <MinusIcon className="size-4" />
              </button>
              {/* Hidden on a phone: six 44px controls already fill the bar. */}
              <output
                className="hidden w-10 text-center text-xs tabular-nums sm:block"
                aria-label={`${noun[0].toUpperCase()}${noun.slice(1)} zoom`}
              >
                {Math.round(zoom * 100)}%
              </output>
              <button
                type="button"
                aria-label="Zoom in"
                title="Zoom in (+)"
                disabled={zoom >= MAX_ZOOM}
                onClick={() => changeZoom(1)}
                className={control}
              >
                <PlusIcon className="size-4" />
              </button>
              <button
                type="button"
                aria-label={`Fit ${noun}`}
                title={`Fit ${noun} (0)`}
                onClick={() => setZoom(1)}
                className={`${control} text-xs`}
              >
                Fit
              </button>
              <button
                type="button"
                aria-label="Actual size"
                title="Actual size (1)"
                // Nothing to enlarge to when the whole picture already fits.
                disabled={actual <= 1 || zoom === actual}
                onClick={() => setZoom(actual)}
                className={`${control} text-xs`}
              >
                1:1
              </button>
              {href && (
                <a
                  href={href}
                  target="_blank"
                  rel="noreferrer"
                  aria-label="Open original in a new tab"
                  title="Open original in a new tab"
                  className={control}
                >
                  <ExternalLinkIcon className="size-4" />
                </a>
              )}
              <button
                type="button"
                aria-label="Close full screen"
                title="Close (Escape)"
                onClick={onClose}
                className={control}
              >
                <XIcon className="size-4" />
              </button>
            </div>
          </div>
          <div
            ref={mountScroller}
            role="region"
            aria-label={`Expanded ${title}`}
            tabIndex={0}
            className="scroll-thin focus-visible:ring-ring min-h-0 flex-1 overflow-auto overscroll-contain outline-none focus-visible:ring-2 focus-visible:ring-inset pb-[env(safe-area-inset-bottom)]"
          >
            <div
              className="flex min-h-full min-w-full items-center justify-center p-4"
              style={{
                width: width ? Math.max(bounds.width, width + 32) : undefined,
                height: height
                  ? Math.max(bounds.height, height + 32)
                  : undefined,
              }}
            >
              <img
                src={image}
                alt={alt}
                className="max-w-none select-none"
                draggable={false}
                style={{
                  width: width || undefined,
                  height: height || undefined,
                }}
                onLoad={(event) => {
                  setSize({
                    width: event.currentTarget.naturalWidth,
                    height: event.currentTarget.naturalHeight,
                  });
                }}
              />
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
