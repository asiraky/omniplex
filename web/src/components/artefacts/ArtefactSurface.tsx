import {
  CheckIcon,
  ChevronDownIcon,
  CodeIcon,
  CopyIcon,
  DownloadIcon,
  EllipsisIcon,
  EyeIcon,
  ExternalLinkIcon,
  Share2Icon,
} from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

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
  useArtefactText,
} from "~/components/artefacts/viewers";
import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import {
  delimiterFor,
  hasSourceView,
  loadViewMode,
  pickVersion,
  rawUrl,
  readsText,
  requestPreview,
  requestShare,
  retoken,
  saveViewMode,
  viewerFor,
  type Artefact,
  type ArtefactVersion,
  type ExpiringUrl,
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

/** A preview URL for an HTML version, kept fresh while the surface is open. */
function usePreview(sessionId: string, artefactId: string, version: number, enabled: boolean) {
  const [state, setState] = useState<PreviewState & { key: string }>({ key: "" });
  const [attempt, setAttempt] = useState(0);
  const key = `${sessionId}/${artefactId}@${version}`;

  useEffect(() => {
    if (!enabled) return;
    let stale = false;
    requestPreview(sessionId, artefactId, version)
      .then((preview) => !stale && setState({ key, preview }))
      .catch((e: unknown) => !stale && setState({ key, error: e instanceof Error ? e.message : String(e) }));
    return () => {
      stale = true;
    };
  }, [sessionId, artefactId, version, enabled, attempt, key]);

  const refresh = useCallback(async () => {
    const preview = await requestPreview(sessionId, artefactId, version);
    setState({ key, preview });
    return preview;
  }, [sessionId, artefactId, version, key]);

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
 * One artefact in the panel: which version, the ways out of the app (download,
 * a new tab, a share link), and the file itself in whichever viewer suits it.
 */
export function ArtefactSurface({
  sessionId,
  artefact,
  version,
  onVersionChange,
}: {
  sessionId: string;
  artefact: Artefact;
  /** Omitted: the latest. */
  version?: number;
  onVersionChange: (version: number) => void;
}) {
  const v = pickVersion(artefact, version);
  const kind: ViewerKind = v ? viewerFor(v.entry, v.mediaType) : "fallback";
  const [mode, setMode] = useViewMode(kind);
  const n = v?.version ?? 0;
  const raw = v ? rawUrl(sessionId, artefact.id, n, v.entry) : "";
  const download = v ? rawUrl(sessionId, artefact.id, n, v.entry, true) : "";
  const text = useArtefactText(raw, Boolean(v) && readsText(kind, mode));
  const preview = usePreview(sessionId, artefact.id, n, Boolean(v) && kind === "html");
  // Where the mini browser is, so a new tab opens the page being looked at.
  const [location, setLocation] = useState<{ key: string; url: string } | null>(null);
  const locationKey = `${artefact.id}@${n}`;

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

  if (!v) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center px-6 text-center text-[13px]">
        This artefact has no versions yet.
      </div>
    );
  }

  const latest = artefact.versions[artefact.versions.length - 1]!;
  const isLatest = v.version === latest.version;
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
            title={artefact.name}
            preview={preview.preview}
            refreshPreview={preview.refresh}
            onLocationChange={(url) => setLocation({ key: locationKey, url })}
          />
        </Suspense>
      );
    }
    if (kind === "image" || (kind === "svg" && mode === "preview")) return <ImageView src={raw} name={artefact.name} />;
    if (kind === "audio" || kind === "video") return <MediaView src={raw} kind={kind} />;
    if (kind === "pdf") return <PdfView src={raw} downloadUrl={download} name={artefact.name} size={v.size} />;
    if (kind === "fallback") {
      return <FallbackView name={artefact.name} mediaType={v.mediaType} size={v.size} src={raw} downloadUrl={download} />;
    }
    // Everything left reads the file as text.
    if (text.status === "error") return <Failure message={text.error} onRetry={text.retry} />;
    if (text.status !== "ready") return <Loading label={`Reading ${v.entry}…`} />;
    if (mode === "source") return <CodeView text={text.text} truncated={text.truncated} />;
    if (kind === "markdown") return <MarkdownView text={text.text} truncated={text.truncated} />;
    if (kind === "csv") return <CsvView text={text.text} delimiter={delimiterFor(v.entry, v.mediaType)} truncated={text.truncated} />;
    if (kind === "json") return <JsonView text={text.text} truncated={text.truncated} />;
    return <CodeView text={text.text} truncated={text.truncated} />;
  })();

  // Frames and players size to the surface; everything else scrolls in it.
  const fills = (kind === "html" && mode === "preview") || (kind === "csv" && mode === "preview") || kind === "video" || kind === "pdf";

  return (
    <div className="@container flex h-full min-h-0 flex-col">
      {/* One row, so a phone spends its height on the file. The name is also
          the version picker; the rarer ways out sit behind the ⋯. */}
      <header className="flex min-w-0 items-center gap-1 border-b py-1 pr-1 pl-2">
        <TypeBadge name={v.entry || artefact.name} mediaType={v.mediaType} className="hidden size-8 @xs:grid" />
        <VersionPicker artefact={artefact} version={v} onChange={onVersionChange} />
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
        <SharePopover
          key={`${artefact.id}@${v.version}`}
          sessionId={sessionId}
          artefactId={artefact.id}
          name={artefact.name}
          version={v.version}
          isLatest={isLatest}
        />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton label="More" className="size-10 md:size-8">
              <EllipsisIcon />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-48">
            <DropdownMenuItem asChild className="min-h-11 md:min-h-0">
              <a href={download} download={v.files > 1 ? v.entry : artefact.name}>
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

/** The name and what this version is. With more than one version the whole
    block opens the list, so the target is the title rather than a small
    chevron beside it. */
function VersionPicker({
  artefact,
  version: v,
  onChange,
}: {
  artefact: Artefact;
  version: ArtefactVersion;
  onChange: (version: number) => void;
}) {
  const latest = artefact.versions[artefact.versions.length - 1]!.version;
  const many = artefact.versions.length > 1;
  const label = `v${v.version}${v.version === latest ? " · latest" : ""}`;
  const now = Date.now();
  const title = (
    <span className="flex min-w-0 flex-1 flex-col text-left">
      <span className="truncate text-[13px] leading-tight font-medium" title={artefact.name}>
        {artefact.name}
      </span>
      <span className="text-muted-foreground flex min-w-0 items-center gap-1 text-[11px] leading-tight">
        {many && (
          <span className="text-foreground/80 flex shrink-0 items-center gap-0.5 font-medium tabular-nums">
            {label}
            <ChevronDownIcon className="size-3" />
          </span>
        )}
        <span className="truncate" title={v.note}>
          {[
            formatAge(v.publishedAt, now),
            v.source === "upload" ? "uploaded" : "",
            v.files > 1 ? `${v.files} files` : "",
            v.note ?? "",
          ]
            .filter(Boolean)
            .join(" · ")}
        </span>
      </span>
    </span>
  );
  if (!many) return <div className="flex min-h-10 min-w-0 flex-1 items-center px-1 md:min-h-8">{title}</div>;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`Version: ${label}`}
          className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-10 min-w-0 flex-1 items-center rounded-md px-1 outline-none focus-visible:ring-2 md:min-h-8"
        >
          {title}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[min(20rem,calc(100vw-2rem))]">
        {[...artefact.versions].reverse().map((ver) => (
          <DropdownMenuItem
            key={ver.version}
            onSelect={() => onChange(ver.version)}
            className="min-h-11 items-start md:min-h-0"
          >
            <span className="w-4 shrink-0 pt-0.5">{ver.version === v.version && <CheckIcon />}</span>
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-[12.5px] font-medium tabular-nums">
                v{ver.version}
                {ver.version === latest && <span className="text-muted-foreground font-normal"> · latest</span>}
              </span>
              <span className="text-muted-foreground line-clamp-2 text-[11px]">
                {[formatAge(ver.publishedAt, now), ver.source === "upload" ? "uploaded" : "", ver.note ?? ""]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

type ShareMode = "latest" | "version";

/** The system share sheet, where there is one: on a phone that is Messages,
    Slack and the rest, which beats a copied link. */
function canShareNatively(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.share === "function";
}

/**
 * Makes a link anyone can open. "Always latest" follows the artefact as the
 * agent revises it; "This version" pins what is on screen now.
 *
 * The link is made as soon as the popover opens, so Copy and Share run inside
 * the tap: Safari refuses both the clipboard and the share sheet once a
 * network round trip has come between the tap and the call. It is also shown,
 * so it can still be long-pressed and copied by hand.
 */
function SharePopover({
  sessionId,
  artefactId,
  name,
  version,
  isLatest,
}: {
  sessionId: string;
  artefactId: string;
  name: string;
  version: number;
  isLatest: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<ShareMode>(isLatest ? "latest" : "version");
  const [links, setLinks] = useState<Partial<Record<ShareMode, ExpiringUrl>>>({});
  const [error, setError] = useState("");
  const { copied, copy } = useCopy();
  const inFlight = useRef<Partial<Record<ShareMode, Promise<ExpiringUrl>>>>({});
  const known = links[mode];
  const link = known && known.expiresAt > Date.now() ? known : undefined;

  const ensure = useCallback(
    (m: ShareMode) => {
      const pending = inFlight.current[m];
      if (pending) return pending;
      const made = requestShare(sessionId, artefactId, m === "latest" ? undefined : version).then(
        (made) => {
          setLinks((all) => ({ ...all, [m]: made }));
          return made;
        },
        (e: unknown) => {
          setError(e instanceof Error ? e.message : String(e));
          throw e;
        },
      );
      inFlight.current[m] = made;
      void made.catch(() => {}).finally(() => delete inFlight.current[m]);
      return made;
    },
    [sessionId, artefactId, version],
  );

  useEffect(() => {
    if (open && !link) void ensure(mode).catch(() => {});
  }, [open, mode, link, ensure]);

  const withLink = async (use: (url: string) => Promise<unknown>) => {
    setError("");
    try {
      await use((link ?? (await ensure(mode))).url);
    } catch (e) {
      // Dismissing the share sheet is not an error worth showing.
      if (e instanceof Error && e.name !== "AbortError") setError(e.message);
    }
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <IconButton label="Share" className="size-10 md:size-8">
          <Share2Icon />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent align="end" className="flex w-[min(20rem,calc(100vw-1rem))] flex-col gap-3 p-3">
        <div>
          <p className="text-[13px] font-medium">Share a link</p>
          <p className="text-muted-foreground text-[11.5px]">Anyone with the link can view it for 7 days.</p>
        </div>
        <div role="radiogroup" aria-label="Link to" className="bg-muted grid grid-cols-2 rounded-md p-0.5">
          {(
            [
              ["latest", "Always latest"],
              ["version", `This version (v${version})`],
            ] as const
          ).map(([m, label]) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={mode === m}
              onClick={() => setMode(m)}
              className={cn(
                "focus-visible:ring-ring min-h-10 rounded px-2 text-[12px] outline-none focus-visible:ring-2 md:min-h-8",
                mode === m ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
              )}
            >
              {label}
            </button>
          ))}
        </div>
        <input
          readOnly
          value={link?.url ?? ""}
          placeholder="Making a link…"
          aria-label="Share link"
          onFocus={(e) => e.currentTarget.select()}
          className="bg-muted/50 w-full rounded-md border px-2 py-1.5 font-mono text-[16px] md:text-[11px]"
        />
        {error && (
          <p role="alert" className="text-destructive text-[11.5px]">
            {error}
          </p>
        )}
        <div className="flex gap-2">
          <Button className="flex-1" variant={canShareNatively() ? "outline" : "default"} onClick={() => void withLink(copy)}>
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? "Copied" : "Copy link"}
          </Button>
          {canShareNatively() && (
            <Button className="flex-1" onClick={() => void withLink((url) => navigator.share({ title: name, url }))}>
              <Share2Icon /> Share…
            </Button>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

export default ArtefactSurface;
