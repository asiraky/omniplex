import { DownloadIcon, ExternalLinkIcon, RotateCwIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { TypeBadge } from "~/components/artefacts/ArtefactTile";
import { Markdown } from "~/components/Markdown";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { formatBytes } from "~/lib/artefacts";
import { parseDelimited } from "~/lib/csv";
import { cn } from "~/lib/utils";
import { useIsCoarsePointer, useIsDesktop } from "~/useMediaQuery";

// ---- reading a file as text ----

/** The most of a file the text viewers read. Past this a phone is paying for
    bytes nobody will scroll to, and the DOM for them costs more than the wire. */
export const TEXT_CAP = 1024 * 1024;

export interface TextRead {
  text: string;
  /** The file goes on past what was read. */
  truncated: boolean;
}

// Versions never change once published, so a URL's text is good for as long
// as the tab lives. Small and bounded: this only exists so that flipping
// between preview and source, or back to a version just looked at, is instant.
const cache = new Map<string, TextRead>();
const CACHE_ENTRIES = 8;

function remember(url: string, read: TextRead) {
  cache.delete(url);
  cache.set(url, read);
  while (cache.size > CACHE_ENTRIES) cache.delete(cache.keys().next().value!);
}

/** Only for tests, which each want to see the fetch happen. */
export function forgetTextCache() {
  cache.clear();
}

/**
 * Reads at most TEXT_CAP bytes. Asks for only that range, and stops reading if
 * a server that ignores Range sends the whole thing anyway.
 */
export async function readText(url: string, signal?: AbortSignal): Promise<TextRead> {
  const res = await fetch(url, { signal, headers: { Range: `bytes=0-${TEXT_CAP}` } });
  // An empty file has no byte 0 to start a range at.
  if (res.status === 416) return { text: "", truncated: false };
  if (!res.ok) {
    const message = await res
      .json()
      .then((b: { error?: string }) => b.error ?? "")
      .catch(() => "");
    throw new Error(message || `Could not read the file (${res.status})`);
  }
  const chunks: Uint8Array[] = [];
  let total = 0;
  const reader = res.body?.getReader();
  if (reader) {
    while (total <= TEXT_CAP) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      total += value.byteLength;
    }
    if (total > TEXT_CAP) void reader.cancel().catch(() => {});
  } else {
    const all = new Uint8Array(await res.arrayBuffer());
    chunks.push(all);
    total = all.byteLength;
  }
  const bytes = new Uint8Array(Math.min(total, TEXT_CAP));
  let at = 0;
  for (const c of chunks) {
    if (at >= bytes.length) break;
    const part = c.subarray(0, bytes.length - at);
    bytes.set(part, at);
    at += part.byteLength;
  }
  const range = /\/(\d+)$/.exec(res.headers.get("Content-Range") ?? "");
  const truncated = total > TEXT_CAP || (range !== null && Number(range[1]) > TEXT_CAP);
  return { text: new TextDecoder().decode(bytes), truncated };
}

export type TextState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; error: string }
  | ({ status: "ready" } & TextRead);

/** The file's text, fetched when `enabled`, with a retry for when 4G drops it. */
export function useArtefactText(url: string, enabled: boolean): TextState & { retry: () => void } {
  const [state, setState] = useState<{ url: string; value: TextState }>({ url: "", value: { status: "idle" } });
  const [attempt, setAttempt] = useState(0);
  const cached = cache.get(url);

  useEffect(() => {
    if (!enabled || cache.has(url)) return;
    const ctl = new AbortController();
    setState({ url, value: { status: "loading" } });
    readText(url, ctl.signal)
      .then((read) => {
        remember(url, read);
        setState({ url, value: { status: "ready", ...read } });
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "error", error: e instanceof Error ? e.message : String(e) } });
      });
    return () => ctl.abort();
  }, [url, enabled, attempt]);

  const retry = () => setAttempt((n) => n + 1);
  if (!enabled) return { status: "idle", retry };
  if (cached) return { status: "ready", ...cached, retry };
  // State left from another URL is not this one's.
  if (state.url !== url) return { status: "loading", retry };
  return { ...state.value, retry };
}

// ---- states ----

export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <p className="text-muted-foreground flex items-center gap-2 px-3 py-4 text-[12px]">
      <Spinner className="text-primary size-3.5" /> {label}
    </p>
  );
}

export function Failure({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="text-destructive flex items-start gap-2 px-3 py-3 text-[12px]">
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
      <span className="min-w-0 flex-1 break-words">{message}</span>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry} className="text-foreground shrink-0">
          <RotateCwIcon /> Retry
        </Button>
      )}
    </div>
  );
}

function Notice({ children }: { children: React.ReactNode }) {
  return <p className="text-muted-foreground border-t px-3 py-2 text-[11px] italic">{children}</p>;
}

const CUT_NOTICE = `Only the first ${formatBytes(TEXT_CAP)} is shown. Download the file for the rest.`;

// ---- text ----

// Lines are rendered in blocks the browser may skip laying out while they are
// off screen, so a megabyte of log does not stall a phone on first paint.
const BLOCK = 300;
const LINE_PX = 19;

/** Monospace text with line numbers, wrapped so a phone never scrolls sideways
    to read a line. */
export function CodeView({ text, truncated = false }: { text: string; truncated?: boolean }) {
  const lines = useMemo(() => {
    const all = text.split("\n");
    // A trailing newline yields one phantom empty line nobody wrote.
    if (all.length > 1 && all[all.length - 1] === "") all.pop();
    return all;
  }, [text]);
  // The padding is inside the width (border-box), so it is added, not
  // counted in ch: at 11.5px a 12px pad is nearly two digits.
  const gutter = `calc(${String(lines.length).length}ch + 1.25rem)`;
  const blocks: string[][] = [];
  for (let i = 0; i < lines.length; i += BLOCK) blocks.push(lines.slice(i, i + BLOCK));

  return (
    <div>
      <div className="py-1 font-mono text-[11.5px] leading-[19px]">
        {blocks.map((block, b) => (
          <div
            key={b}
            style={{ contentVisibility: "auto", containIntrinsicSize: `auto ${block.length * LINE_PX}px` }}
          >
            {block.map((line, i) => (
              <div key={i} className="flex">
                <span
                  aria-hidden
                  style={{ width: gutter }}
                  className="text-muted-foreground/50 shrink-0 pr-3 text-right select-none"
                >
                  {b * BLOCK + i + 1}
                </span>
                <span className="min-w-0 flex-1 pr-3 break-words whitespace-pre-wrap">{line || " "}</span>
              </div>
            ))}
          </div>
        ))}
      </div>
      {truncated && <Notice>{CUT_NOTICE}</Notice>}
    </div>
  );
}

export function MarkdownView({ text, truncated = false }: { text: string; truncated?: boolean }) {
  return (
    <div>
      <Markdown document text={text} className="mx-auto max-w-3xl px-4 py-3 text-[14px] leading-relaxed" />
      {truncated && <Notice>{CUT_NOTICE}</Notice>}
    </div>
  );
}

/** Pretty-printed, or the raw text with a reason when it will not parse. */
export function JsonView({ text, truncated = false }: { text: string; truncated?: boolean }) {
  const pretty = useMemo(() => {
    try {
      return { text: JSON.stringify(JSON.parse(text), null, 2) };
    } catch (e) {
      return { error: e instanceof Error ? e.message : String(e) };
    }
  }, [text]);
  if (pretty.text !== undefined) return <CodeView text={pretty.text} truncated={truncated} />;
  return (
    <div>
      <p className="text-muted-foreground border-b px-3 py-2 text-[11px]">
        {truncated ? "Cut short, so it cannot be formatted." : `Not valid JSON (${pretty.error}), shown as written.`}
      </p>
      <CodeView text={text} truncated={truncated} />
    </div>
  );
}

// ---- tables ----

/** Rows past this are not rendered: a table is for looking at, and 2,000 rows
    is well past what anyone reads on a screen. */
export const MAX_TABLE_ROWS = 2000;

export function CsvView({ text, delimiter, truncated = false }: { text: string; delimiter: string; truncated?: boolean }) {
  // One more than the cap: the header row is not a data row.
  const parsed = useMemo(() => parseDelimited(text, delimiter, MAX_TABLE_ROWS + 1), [text, delimiter]);
  const [head, ...body] = parsed.rows;
  if (!head) return <p className="text-muted-foreground px-3 py-4 text-[12px]">The file is empty.</p>;
  const columns = Math.max(head.length, ...body.map((r) => r.length));

  // The table scrolls both ways in one box, so the header row can stick: an
  // inner sideways scroller would capture it and it would scroll off.
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="scroll-thin scroll-shadow-x min-h-0 flex-1 overflow-auto overscroll-contain">
        <table className="w-max min-w-full border-collapse text-[12px]">
          <thead className="bg-muted sticky top-0 z-10">
            <tr>
              <th className="text-muted-foreground/60 w-[1%] border-b px-2 py-1.5 text-right font-normal">#</th>
              {Array.from({ length: columns }, (_, c) => (
                <th key={c} className="max-w-64 truncate border-b border-l px-2.5 py-1.5 text-left font-semibold" title={head[c]}>
                  {head[c] ?? ""}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {body.map((row, r) => (
              <tr key={r} className="hover:bg-accent/40">
                <td className="text-muted-foreground/60 border-b px-2 py-1 text-right font-mono tabular-nums">{r + 1}</td>
                {Array.from({ length: columns }, (_, c) => (
                  <td key={c} className="max-w-64 truncate border-b border-l px-2.5 py-1 align-top" title={row[c]}>
                    {row[c] ?? ""}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {(parsed.truncated || truncated) && (
        <Notice>
          {parsed.truncated
            ? `Showing the first ${MAX_TABLE_ROWS.toLocaleString()} rows. Download the file for the rest.`
            : CUT_NOTICE}
        </Notice>
      )}
    </div>
  );
}

// ---- media ----

/** Fitted to the panel's width; a tap shows it at its own size. */
export function ImageView({ src, name }: { src: string; name: string }) {
  const [actual, setActual] = useState(false);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const img = useRef<HTMLImageElement>(null);
  // Where the tap landed, as a fraction of the fitted image and a point on
  // screen: zooming in keeps that spot under the finger, not the top-left.
  const focus = useRef<{ fx: number; fy: number; x: number; y: number } | null>(null);
  useLayoutEffect(() => {
    const f = focus.current;
    focus.current = null;
    const el = img.current;
    const scroller = el && actual && f ? scrollParent(el) : null;
    if (!el || !f || !scroller) return;
    const r = el.getBoundingClientRect();
    scroller.scrollLeft += r.left + f.fx * r.width - f.x;
    scroller.scrollTop += r.top + f.fy * r.height - f.y;
  }, [actual]);
  return (
    <div className="flex min-h-full flex-col">
      {state === "loading" && <Loading label="Loading image…" />}
      {state === "error" && <Failure message="The image could not be loaded." />}
      <button
        type="button"
        onClick={(e) => {
          const r = img.current?.getBoundingClientRect();
          // A keyboard press (detail 0) has no point; it zooms from the corner.
          if (!actual && r && e.detail > 0 && r.width > 0 && r.height > 0) {
            focus.current = {
              fx: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
              fy: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
              x: e.clientX,
              y: e.clientY,
            };
          }
          setActual((v) => !v);
        }}
        aria-pressed={actual}
        aria-label={actual ? "Fit to width" : "Show at actual size"}
        className={cn(
          "focus-visible:ring-ring m-auto block p-3 outline-none focus-visible:ring-2 focus-visible:ring-inset",
          actual ? "cursor-zoom-out" : "cursor-zoom-in",
          state !== "ready" && "sr-only",
        )}
      >
        <img
          ref={img}
          src={src}
          alt={name}
          onLoad={() => setState("ready")}
          onError={() => setState("error")}
          className={cn(
            "rounded-md bg-[repeating-conic-gradient(var(--muted)_0_25%,transparent_0_50%)] bg-[length:16px_16px]",
            actual ? "max-w-none" : "h-auto max-w-full",
          )}
        />
      </button>
    </div>
  );
}

function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const { overflowX, overflowY } = getComputedStyle(p);
    if (/auto|scroll/.test(overflowX + overflowY)) return p;
  }
  return null;
}

export function MediaView({ src, kind }: { src: string; kind: "audio" | "video" }) {
  const [error, setError] = useState(false);
  if (error) return <Failure message={`This ${kind} could not be played in the browser. Download it instead.`} />;
  if (kind === "audio") {
    return (
      <div className="flex items-center justify-center p-6">
        <audio controls preload="metadata" src={src} onError={() => setError(true)} className="w-full max-w-xl" />
      </div>
    );
  }
  return (
    <div className="flex h-full items-center justify-center bg-black">
      <video
        controls
        playsInline
        preload="metadata"
        src={src}
        onError={() => setError(true)}
        className="max-h-full max-w-full"
      />
    </div>
  );
}

/**
 * Inline on a desktop. A phone gets a card instead: mobile browsers render a
 * PDF in a frame as its first page, unscrollable, or not at all — while opening
 * it hands it to the system viewer, which is good at exactly this.
 */
export function PdfView({
  src,
  downloadUrl,
  name,
  size,
}: {
  src: string;
  downloadUrl: string;
  name: string;
  size: number;
}) {
  const desktop = useIsDesktop();
  const coarse = useIsCoarsePointer();
  if (desktop && !coarse) return <iframe src={src} title={name} className="size-full border-0 bg-white" />;
  return (
    <FileCard name={name} mediaType="application/pdf" size={size}>
      <Button asChild className="min-w-32">
        <a href={src} target="_blank" rel="noopener noreferrer">
          <ExternalLinkIcon /> Open PDF
        </a>
      </Button>
      <Button asChild variant="outline" className="min-w-32">
        <a href={downloadUrl} download={name}>
          <DownloadIcon /> Download
        </a>
      </Button>
    </FileCard>
  );
}

function FileCard({
  name,
  mediaType,
  size,
  note,
  children,
}: {
  name: string;
  mediaType: string;
  size: number;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-full flex-col items-center justify-center gap-3 px-6 py-10 text-center">
      <TypeBadge name={name} mediaType={mediaType} className="size-16 rounded-xl text-[13px]" />
      <div className="flex max-w-full flex-col gap-1">
        <p className="text-[14px] font-medium break-all">{name}</p>
        <p className="text-muted-foreground text-[12px]">
          {formatBytes(size)} · {mediaType}
        </p>
      </div>
      {note && <p className="text-muted-foreground max-w-72 text-[12px]">{note}</p>}
      <div className="flex flex-wrap justify-center gap-2">{children}</div>
    </div>
  );
}

export function FallbackView({
  name,
  mediaType,
  size,
  src,
  downloadUrl,
}: {
  name: string;
  mediaType: string;
  size: number;
  src: string;
  downloadUrl: string;
}) {
  return (
    <FileCard
      name={name}
      mediaType={mediaType}
      size={size}
      note="There is no in-app preview for this type yet."
    >
      <Button asChild className="min-w-32">
        <a href={downloadUrl} download={name}>
          <DownloadIcon /> Download
        </a>
      </Button>
      <Button asChild variant="outline" className="min-w-32">
        <a href={src} target="_blank" rel="noopener noreferrer">
          <ExternalLinkIcon /> Open
        </a>
      </Button>
    </FileCard>
  );
}
