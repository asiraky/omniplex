import {
  CheckIcon,
  CodeIcon,
  CopyIcon,
  DownloadIcon,
  EllipsisIcon,
  EyeIcon,
  ExternalLinkIcon,
  FolderIcon,
  Share2Icon,
} from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { useArtefactText } from "~/components/artefacts/artefactText";
import { TypeBadge } from "~/components/artefacts/ArtefactTile";
import {
  CodeView,
  CsvView,
  FallbackView,
  Failure,
  ImageView,
  JsonView,
  Loading,
  MarkdownView,
  MediaView,
  PdfView,
} from "~/components/artefacts/viewers";
import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import {
  delimiterFor,
  hasSourceView,
  getShare,
  loadViewMode,
  rawUrl,
  readsText,
  requestPreview,
  retoken,
  saveViewMode,
  share,
  unshare,
  viewerFor,
  type Artefact,
  type ExpiringUrl,
  type ShareLink,
  type ViewerKind,
  type ViewMode,
} from "~/lib/artefacts";
import { useCopy } from "~/lib/clipboard";
import { formatAge } from "~/lib/usageFormat";
import { cn } from "~/lib/utils";

// The browser chrome, its console and its device frames are only wanted once
// an HTML artefact is on screen.
const MiniBrowser = lazy(() => import("~/components/artefacts/MiniBrowser"));

// Refresh a preview token this long before it lapses, so "open in a new tab"
// never hands out a dead link. The frame itself is not reloaded for it.
const REFRESH_BEFORE_MS = 5 * 60_000;

interface PreviewState {
  preview?: ExpiringUrl;
  error?: string;
}

/** A preview URL for an HTML artefact, kept fresh while the surface is open.
    The URL serves the live files, so a revision needs no new one. */
function usePreview(threadId: string, artefactId: string, enabled: boolean) {
  const [state, setState] = useState<PreviewState & { key: string }>({ key: "" });
  const [attempt, setAttempt] = useState(0);
  const key = `${threadId}/${artefactId}`;

  useEffect(() => {
    if (!enabled) return;
    let stale = false;
    requestPreview(threadId, artefactId)
      .then((preview) => !stale && setState({ key, preview }))
      .catch((e: unknown) => !stale && setState({ key, error: e instanceof Error ? e.message : String(e) }));
    return () => {
      stale = true;
    };
  }, [threadId, artefactId, enabled, attempt, key]);

  const refresh = useCallback(async () => {
    const preview = await requestPreview(threadId, artefactId);
    setState({ key, preview });
    return preview;
  }, [threadId, artefactId, key]);

  const current = state.key === key ? state : { key };
  useEffect(() => {
    if (!current.preview) return;
    const wait = Math.max(10_000, current.preview.expiresAt - Date.now() - REFRESH_BEFORE_MS);
    const t = setTimeout(() => void refresh().catch(() => {}), wait);
    return () => clearTimeout(t);
  }, [current.preview, refresh]);

  return { ...current, refresh, retry: () => setAttempt((n) => n + 1) };
}

/** Preview or source for this kind of file, remembered per kind. */
function useViewMode(kind: ViewerKind): [ViewMode, (mode: ViewMode) => void] {
  const [chosen, setChosen] = useState<{ kind: ViewerKind; mode: ViewMode }>(() => ({ kind, mode: loadViewMode(kind) }));
  const mode = chosen.kind === kind ? chosen.mode : loadViewMode(kind);
  const set = useCallback(
    (next: ViewMode) => {
      saveViewMode(kind, next);
      setChosen({ kind, mode: next });
    },
    [kind],
  );
  return [hasSourceView(kind) ? mode : "preview", set];
}

/**
 * One artefact in the panel: the file as it is on disk now, the ways out of
 * the app (download, a new tab, a share link), and where it lives.
 */
export function ArtefactSurface({ threadId, artefact: a }: { threadId: string; artefact: Artefact }) {
  const kind: ViewerKind = viewerFor(a.entry, a.mediaType);
  const [mode, setMode] = useViewMode(kind);
  const raw = rawUrl(threadId, a.id, a.entry, { rev: a.modifiedAt });
  const download = rawUrl(threadId, a.id, a.entry, { rev: a.modifiedAt, download: true });
  const text = useArtefactText(raw, readsText(kind, mode));
  const preview = usePreview(threadId, a.id, kind === "html");
  // Where the mini browser is, so a new tab opens the page being looked at.
  // Keyed on the revision: when the agent shows the page again, the browser
  // starts over on it.
  const [location, setLocation] = useState<{ key: string; url: string } | null>(null);
  const locationKey = `${a.id}@${a.modifiedAt}`;
  const { copied: pathCopied, copy: copyPath } = useCopy();

  // Flipping preview and source keeps the reader roughly where they were, by
  // proportion: the two are different heights, but a third of the way down
  // one is near a third of the way down the other.
  const scrollRef = useRef<HTMLDivElement>(null);
  const pendingScroll = useRef<number | null>(null);
  const changeMode = (next: ViewMode) => {
    const el = scrollRef.current;
    if (el) {
      const room = el.scrollHeight - el.clientHeight;
      pendingScroll.current = room > 0 ? el.scrollTop / room : 0;
    }
    setMode(next);
  };
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (pendingScroll.current === null || !el || text.status === "loading") return;
    el.scrollTop = pendingScroll.current * (el.scrollHeight - el.clientHeight);
    pendingScroll.current = null;
  }, [mode, text.status]);

  const openUrl =
    kind === "html"
      ? preview.preview
        ? location?.key === locationKey
          ? retoken(location.url, preview.preview.url)
          : preview.preview.url
        : undefined
      : raw;

  const body = (() => {
    if (kind === "html" && mode === "preview") {
      if (preview.error) return <Failure message={preview.error} onRetry={preview.retry} />;
      if (!preview.preview) return <Loading label="Preparing the preview…" />;
      return (
        <Suspense fallback={<Loading />}>
          <MiniBrowser
            key={locationKey}
            title={a.name}
            preview={preview.preview}
            refreshPreview={preview.refresh}
            onLocationChange={(url) => setLocation({ key: locationKey, url })}
          />
        </Suspense>
      );
    }
    if (kind === "image" || (kind === "svg" && mode === "preview")) return <ImageView src={raw} name={a.name} />;
    if (kind === "audio" || kind === "video") return <MediaView src={raw} kind={kind} />;
    if (kind === "pdf") return <PdfView src={raw} downloadUrl={download} name={a.name} size={a.size} />;
    if (kind === "fallback") {
      return <FallbackView name={a.name} mediaType={a.mediaType} size={a.size} src={raw} downloadUrl={download} />;
    }
    // Everything left reads the file as text.
    if (text.status === "error") return <Failure message={text.error} onRetry={text.retry} />;
    if (text.status !== "ready") return <Loading label={`Reading ${a.entry}…`} />;
    if (mode === "source") return <CodeView text={text.text} truncated={text.truncated} />;
    if (kind === "markdown") return <MarkdownView text={text.text} truncated={text.truncated} />;
    if (kind === "csv") return <CsvView text={text.text} delimiter={delimiterFor(a.entry, a.mediaType)} truncated={text.truncated} />;
    if (kind === "json") return <JsonView text={text.text} truncated={text.truncated} />;
    return <CodeView text={text.text} truncated={text.truncated} />;
  })();

  // Frames and players size to the surface; everything else scrolls in it.
  const fills = (kind === "html" && mode === "preview") || (kind === "csv" && mode === "preview") || kind === "video" || kind === "pdf";

  return (
    <div className="@container flex h-full min-h-0 flex-col">
      {/* One row, so a phone spends its height on the file. The rarer ways
          out, and where the file lives, sit behind the ⋯. */}
      <header className="flex min-w-0 items-center gap-1 border-b py-1 pr-1 pl-2">
        <TypeBadge name={a.entry || a.name} mediaType={a.mediaType} className="hidden size-8 @xs:grid" />
        <Title artefact={a} />
        {hasSourceView(kind) && (
          <div role="radiogroup" aria-label="View" className="bg-muted flex shrink-0 rounded-md p-0.5">
            {(["preview", "source"] as const).map((m) => (
              <button
                key={m}
                type="button"
                role="radio"
                aria-checked={mode === m}
                aria-label={m === "preview" ? "Preview" : "Source"}
                onClick={() => mode !== m && changeMode(m)}
                className={cn(
                  "focus-visible:ring-ring flex h-9 min-w-9 items-center justify-center gap-1.5 rounded px-2 text-[12px] outline-none focus-visible:ring-2 md:h-7",
                  mode === m ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
                )}
              >
                {m === "preview" ? <EyeIcon className="size-3.5" /> : <CodeIcon className="size-3.5" />}
                <span className="hidden @lg:inline">{m === "preview" ? "Preview" : "Source"}</span>
              </button>
            ))}
          </div>
        )}
        <SharePopover key={a.id} threadId={threadId} artefact={a} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton label="More" className="size-10 md:size-8">
              <EllipsisIcon />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-[min(20rem,calc(100vw-1rem))]">
            <DropdownMenuLabel className="text-muted-foreground font-mono text-[11px] font-normal break-all">
              {a.path}
            </DropdownMenuLabel>
            <DropdownMenuItem
              className="min-h-11 md:min-h-0"
              onSelect={(e) => {
                // Stay open long enough to say it worked.
                e.preventDefault();
                void copyPath(a.path);
              }}
            >
              {pathCopied ? <CheckIcon /> : <FolderIcon />} {pathCopied ? "Copied" : "Copy path"}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild className="min-h-11 md:min-h-0">
              <a href={download} download={a.files > 1 ? a.entry : a.name}>
                <DownloadIcon /> Download
              </a>
            </DropdownMenuItem>
            <DropdownMenuItem asChild disabled={!openUrl} className="min-h-11 md:min-h-0">
              <a href={openUrl} target="_blank" rel="noopener noreferrer">
                <ExternalLinkIcon /> Open in a new tab
              </a>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </header>

      <div
        ref={scrollRef}
        className={cn("min-h-0 flex-1", fills ? "overflow-hidden" : "scroll-thin overflow-auto overscroll-contain")}
      >
        {body}
      </div>
    </div>
  );
}

/** The name, and when and why it was last shown. */
function Title({ artefact: a }: { artefact: Artefact }) {
  const detail = [
    formatAge(a.shownAt, Date.now()),
    a.source === "upload" ? "uploaded" : "",
    a.files > 1 ? `${a.files} files` : "",
    a.note ?? "",
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <div className="flex min-h-10 min-w-0 flex-1 flex-col justify-center px-1 md:min-h-8">
      <span className="truncate text-[13px] leading-tight font-medium" title={a.path}>
        {a.name}
      </span>
      <span className="text-muted-foreground truncate text-[11px] leading-tight" title={a.note}>
        {detail}
      </span>
    </div>
  );
}

/** The system share sheet, where there is one: on a phone that is Messages,
    Slack and the rest, which beats a copied link. */
function canShareNatively(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.share === "function";
}

/**
 * A link anyone can open, to a copy of the artefact taken when it was shared.
 * The file stays in the project and the agent can go on changing it; the link
 * shows those changes only once someone updates it. Nothing is shared until
 * someone asks, so opening the popover only asks whether a link exists.
 *
 * Copy and Share use the link already on screen, so they run inside the tap:
 * Safari refuses both the clipboard and the share sheet once a network round
 * trip has come between the tap and the call. That is why making the link and
 * copying it are two taps.
 */
function SharePopover({ threadId, artefact: a }: { threadId: string; artefact: Artefact }) {
  const [open, setOpen] = useState(false);
  // undefined: not asked yet. null: not shared.
  const [link, setLink] = useState<ShareLink | null | undefined>(undefined);
  const [busy, setBusy] = useState<"" | "share" | "stop">("");
  const [error, setError] = useState("");
  const { copied, copy } = useCopy();

  useEffect(() => {
    if (!open) return;
    let stale = false;
    getShare(threadId, a.id).then(
      (l) => !stale && setLink(l),
      (e: unknown) => !stale && setError(e instanceof Error ? e.message : String(e)),
    );
    return () => {
      stale = true;
    };
  }, [open, threadId, a.id]);

  const run = async (what: "share" | "stop") => {
    setBusy(what);
    setError("");
    try {
      if (what === "share") setLink(await share(threadId, a.id));
      else {
        await unshare(threadId, a.id);
        setLink(null);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const withLink = async (use: (url: string) => Promise<unknown>) => {
    if (!live) return;
    setError("");
    try {
      await use(live.url);
    } catch (e) {
      // Dismissing the share sheet is not an error worth showing.
      if (e instanceof Error && e.name !== "AbortError") setError(e.message);
    }
  };

  const now = Date.now();
  const live = link && link.expiresAt > now ? link : null;
  // Only as good as the last time the file was shown: an edit the agent has
  // not shown yet is not known here, which is why Update link is always there.
  const changed = live !== null && a.modifiedAt > live.sharedAt;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <IconButton label="Share" className="size-10 md:size-8">
          <Share2Icon />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent align="end" className="flex w-[min(20rem,calc(100vw-1rem))] flex-col gap-3 p-3">
        {link === undefined ? (
          !error && <p className="text-muted-foreground text-[12px]">Checking…</p>
        ) : !live ? (
          <>
            <div>
              <p className="text-[13px] font-medium">Share a link</p>
              <p className="text-muted-foreground text-[11.5px] leading-snug">
                Anyone with the link sees a copy of this as it is now, for 7 days. Changes after that stay here until
                you update the link.
              </p>
            </div>
            <Button disabled={busy !== ""} onClick={() => void run("share")}>
              <Share2Icon /> {busy === "share" ? "Creating…" : "Create link"}
            </Button>
          </>
        ) : (
          <>
            <div>
              <p className="text-[13px] font-medium">Shared</p>
              <p className="text-muted-foreground text-[11.5px] leading-snug">
                The link shows it as it was {formatAge(live.sharedAt, now)}
                {changed ? ", and it has changed since" : ""}. It works for {timeLeft(live.expiresAt, now)}.
              </p>
            </div>
            <input
              readOnly
              value={live.url}
              aria-label="Share link"
              onFocus={(e) => e.currentTarget.select()}
              className="bg-muted/50 w-full rounded-md border px-2 py-1.5 font-mono text-[16px] md:text-[11px]"
            />
            <div className="flex gap-2">
              <Button
                className="flex-1"
                variant={canShareNatively() ? "outline" : "default"}
                onClick={() => void withLink(copy)}
              >
                {copied ? <CheckIcon /> : <CopyIcon />}
                {copied ? "Copied" : "Copy link"}
              </Button>
              {canShareNatively() && (
                <Button className="flex-1" onClick={() => void withLink((url) => navigator.share({ title: a.name, url }))}>
                  <Share2Icon /> Share…
                </Button>
              )}
            </div>
            <div className="flex gap-2">
              <Button
                className="flex-1"
                variant={changed ? "default" : "outline"}
                disabled={busy !== ""}
                onClick={() => void run("share")}
              >
                {busy === "share" ? "Updating…" : "Update link"}
              </Button>
              <Button className="flex-1" variant="ghost" disabled={busy !== ""} onClick={() => void run("stop")}>
                {busy === "stop" ? "Stopping…" : "Stop sharing"}
              </Button>
            </div>
          </>
        )}
        {error && (
          <p role="alert" className="text-destructive text-[11.5px]">
            {error}
          </p>
        )}
      </PopoverContent>
    </Popover>
  );
}

function timeLeft(expiresAt: number, now: number): string {
  const hours = Math.max(1, Math.round((expiresAt - now) / 3_600_000));
  if (hours < 48) return `${hours} more ${hours === 1 ? "hour" : "hours"}`;
  return `${Math.round(hours / 24)} more days`;
}

export default ArtefactSurface;
