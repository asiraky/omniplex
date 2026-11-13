import { Maximize2Icon, RotateCwIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type Ref } from "react";

import { inlineFrameHeight } from "~/components/artefacts/inlineFrame";
import { usePreview } from "~/components/artefacts/usePreview";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import type { Artefact } from "~/lib/artefacts";
import { useIsDesktop } from "~/useMediaQuery";

// The last height each revision of a page reported, so a frame that unmounts
// off screen, or a transcript that remounts, comes back at the size it was
// rather than at the floor and then jumping.
const heights = new Map<string, number>();

/** jsdom and old browsers: with nothing to say what is near, everything is. */
const canObserve = () => typeof IntersectionObserver !== "undefined";

/**
 * An HTML page the agent showed inline, drawn in the transcript as the visual
 * half of its reply.
 *
 * The page runs in the same sandbox as the viewer's mini browser: a frame
 * without `allow-same-origin` on a tokenised preview URL, so it cannot reach
 * this app. It talks back only through the bridge the preview route injects,
 * and only its `size` message matters here.
 *
 * It costs nothing until it is wanted. The token is fetched the first time the
 * row comes within a screen of the view, and the frame is mounted only while
 * it stays that close: a long thread with ten of these loads the one being
 * read, not ten pages over 4G. Away from the view, the frame's box keeps its
 * height so nothing above or below moves.
 */
export function InlineArtefact({
  threadId,
  artefact: a,
  onOpen,
}: {
  threadId: string;
  artefact: Artefact;
  /** Opens the artefact in the full viewer. */
  onOpen?: () => void;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const frameRef = useRef<HTMLIFrameElement>(null);
  const [near, setNear] = useState(() => !canObserve());
  const [seen, setSeen] = useState(() => !canObserve());
  const preview = usePreview(threadId, a.id, seen);

  const sizeKey = `${a.id}@${a.modifiedAt}`;
  // Not reset when the page is revised: the old page's height is a better
  // guess at the new one's than the floor is.
  const [reported, setReported] = useState(() => heights.get(sizeKey));
  const desktop = useIsDesktop();
  // Read once: a phone's height moves with its toolbars and keyboard, and the
  // frame should not resize under the reader for either.
  const [viewportHeight] = useState(() => window.innerHeight);
  const { height, cut } = inlineFrameHeight(reported, { desktop, viewportHeight });

  useEffect(() => {
    const el = boxRef.current;
    if (!el || !canObserve()) return;
    const io = new IntersectionObserver(
      (entries) => {
        const close = entries[entries.length - 1]!.isIntersecting;
        setNear(close);
        if (close) setSeen(true);
      },
      // A screen either side, so the page has loaded by the time it scrolls in.
      { root: el.closest<HTMLElement>("[data-transcript-scroller]"), rootMargin: "100% 0px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (!frameRef.current || e.source !== frameRef.current.contentWindow) return;
      const d = e.data as Record<string, unknown> | null;
      if (!d || typeof d !== "object" || d.omniplex !== "size") return;
      if (typeof d.height !== "number" || !Number.isFinite(d.height) || d.height < 0) return;
      heights.set(sizeKey, d.height);
      setReported(d.height);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [sizeKey]);

  // The transcript does its own scroll anchoring (overflow-anchor is off), so
  // a frame resizing above the view would shove what the reader is looking
  // at. Moving the scroll by the same amount keeps it still.
  const lastHeight = useRef(height);
  useLayoutEffect(() => {
    const delta = height - lastHeight.current;
    lastHeight.current = height;
    const el = boxRef.current;
    const scroller = el?.closest<HTMLElement>("[data-transcript-scroller]");
    if (!delta || !el || !scroller) return;
    if (el.getBoundingClientRect().top < scroller.getBoundingClientRect().top) scroller.scrollTop += delta;
  }, [height]);

  return (
    <div className="bg-card w-full overflow-hidden rounded-xl border">
      <InlineHeader name={a.name} path={a.path} onOpen={onOpen} />
      <div ref={boxRef} className="bg-muted/40 relative" style={{ height }}>
        {preview.error ? (
          <div role="alert" className="text-destructive flex items-start gap-2 px-3 py-3 text-[12px]">
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
            <span className="min-w-0 flex-1 break-words">{preview.error}</span>
            <Button variant="outline" size="sm" onClick={preview.retry} className="text-foreground shrink-0">
              <RotateCwIcon /> Retry
            </Button>
          </div>
        ) : near && preview.preview ? (
          <PageFrame
            // A new showing reloads the page, even when the file is unchanged.
            key={`${a.modifiedAt}@${a.shownAt}`}
            frameRef={frameRef}
            url={preview.preview.url}
            title={a.name}
            height={height}
          />
        ) : (
          near && <LoadingBadge />
        )}
        {cut && !preview.error && (
          <div className="from-card pointer-events-none absolute inset-x-0 bottom-0 flex h-20 items-end justify-center bg-gradient-to-t to-transparent pb-2">
            {onOpen && (
              <Button variant="outline" size="sm" onClick={onOpen} className="pointer-events-auto min-h-11 shadow-sm md:min-h-8">
                <Maximize2Icon /> Expand
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** The page's name, and the way to the full viewer whether or not it was cut. */
function InlineHeader({ name, path, onOpen }: { name: string; path: string; onOpen?: () => void }) {
  const body = (
    <>
      <span className="min-w-0 flex-1 truncate" title={path}>
        {name}
      </span>
      {onOpen && <Maximize2Icon aria-hidden className="text-muted-foreground size-3.5 shrink-0" />}
    </>
  );
  const shape = "flex min-h-11 w-full items-center gap-2 border-b px-3 text-left text-[12.5px] font-medium md:min-h-9";
  if (!onOpen) return <div className={shape}>{body}</div>;
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`Open ${name}`}
      className={`${shape} hover:bg-accent/50 focus-visible:ring-ring cursor-pointer transition-colors outline-none focus-visible:ring-inset focus-visible:ring-2`}
    >
      {body}
    </button>
  );
}

function LoadingBadge() {
  return (
    <span className="bg-background/80 absolute top-2 right-2 rounded-full p-1 shadow-sm">
      <Spinner className="text-primary size-3.5" />
    </span>
  );
}

/** One load of the page. Remounted to reload, never re-pointed: a refreshed
    token is for the next mount, not a reason to reload what is showing. */
function PageFrame({
  frameRef,
  url,
  title,
  height,
}: {
  frameRef: Ref<HTMLIFrameElement>;
  url: string;
  title: string;
  height: number;
}) {
  // react-doctor-disable-next-line react-doctor/no-derived-useState -- deliberately captured once; re-syncing would reload the page on every token refresh
  const [src] = useState(url);
  const [loading, setLoading] = useState(true);
  return (
    <>
      <iframe
        ref={frameRef}
        src={src}
        title={title}
        // Never allow-same-origin: with it, the page and this app would share
        // an origin, and the agent's HTML could drive the API.
        sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads"
        referrerPolicy="no-referrer"
        // The page never scrolls inside itself, so a drag over it scrolls the
        // transcript; what is past the cap is the full viewer's to show.
        scrolling="no"
        onLoad={() => setLoading(false)}
        className="block w-full border-0 bg-white"
        style={{ height }}
      />
      {loading && <LoadingBadge />}
    </>
  );
}
