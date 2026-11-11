import {
  ArrowRightIcon,
  BotIcon,
  BrainIcon,
  CheckIcon,
  ChevronDownIcon,
  CircleIcon,
  CircleSlashIcon,
  DownloadIcon,
  FileTextIcon,
  PencilIcon,
  SearchIcon,
  ShieldXIcon,
  TerminalIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { lazy, Suspense, useMemo, useState, type ComponentType } from "react";

import { Markdown } from "~/components/Markdown";
import { Spinner } from "~/components/ui/spinner";
import { parsePatch } from "~/lib/patch";
import { toolImages } from "~/lib/toolImages";
import { cn } from "~/lib/utils";
import type { Item, ToolContent, ToolStatus, Turn } from "~/protocol";
import { foldLabel, summarise } from "~/rows";

import { LiveTail } from "./LiveTail";
import type { FullToolBody } from "./LoadFullOutput";
import { ToolImages } from "./ToolImages";

// Few cards are truncated, so the loader arrives on first open. On a dropped
// connection or after a redeploy its chunk can fail to load, and with no error
// boundary above it that would take the app down; the card says so instead.
function LoaderUnavailable() {
  return (
    <p role="alert" className="text-destructive text-[12px]">
      Could not load the full output. Reload the page to try again.
    </p>
  );
}
const LoadFullOutput = lazy(() => import("./LoadFullOutput").catch(() => ({ default: LoaderUnavailable })));

// A diff shows only once its card is opened, so its renderer stays out of the
// initial bundle.
const ToolDiff = lazy(() => import("./ToolDiff").then((m) => ({ default: m.ToolDiff })));

// One icon per tool kind the protocol defines. Anything new falls through to
// the neutral dot rather than rendering nothing.
const TOOL_ICON: Record<string, ComponentType<{ className?: string }>> = {
  read: FileTextIcon,
  edit: PencilIcon,
  delete: Trash2Icon,
  move: ArrowRightIcon,
  search: SearchIcon,
  execute: TerminalIcon,
  think: BrainIcon,
  agent: BotIcon,
  fetch: DownloadIcon,
  other: CircleIcon,
};

export function StatusMark({ status }: { status?: ToolStatus }) {
  if (status === "in_progress" || status === "pending")
    return <Spinner className="text-primary size-3.5" />;
  if (status === "failed")
    return <XIcon aria-label="Failed" className="text-destructive size-3.5" />;
  // Stopped on purpose, not broken: muted, not red.
  if (status === "cancelled")
    return <CircleSlashIcon aria-label="Cancelled" className="text-muted-foreground size-3.5" />;
  // Refused by the user, so it never ran: neither done nor broken.
  if (status === "declined")
    return <ShieldXIcon aria-label="Declined" className="text-muted-foreground size-3.5" />;
  return <CheckIcon aria-label="Done" className="text-success size-3.5" />;
}

const running = (item: Item | undefined) =>
  item?.status === "in_progress" || item?.status === "pending";

// What a tool card shows: the full output and input once fetched, standing in
// for the previews, else the previews. The full body is held by the card only,
// so it goes when the card does. It is the call as it stood when loaded: once
// more output lands (a call still running) it is stale, and the previews and
// the load come back.
function useToolOutput(item: Item) {
  const [full, setFull] = useState<FullToolBody>();
  const [loadedOver, setLoadedOver] = useState(item.content);
  if (loadedOver !== item.content) {
    setLoadedOver(item.content);
    setFull(undefined);
  }
  return {
    setFull,
    content: (full ? full.content : item.content) ?? [],
    input: full && full.input !== undefined ? full.input : item.input,
    truncated: !!item.truncated && !full,
  };
}

// What a card's body shows, in the order the tool produced it. Neighbouring
// text pieces read as one output, so they share a box. Images are not here:
// they show as thumbnails whether or not the card is open.
type Block =
  | { kind: "text"; text: string; path?: string }
  | { kind: "diff"; path?: string; patch: string };

// A preview of a long patch is cut in the middle (capToolText). The marker it
// leaves becomes a patch note, so it reads as a gap rather than a line of the
// file.
const PREVIEW_CUT = /\n\n(\[… \d+ characters truncated …\])\n\n/;

function blocks(content: ToolContent[]): Block[] {
  const out: Block[] = [];
  for (const c of content) {
    const text = c.text ?? "";
    // A "diff" with no hunk is a whole file (Codex sends an added or deleted
    // file's contents as-is), and read as a patch it would lose every line's
    // first character.
    if (c.type === "diff" && /^@@ /m.test(text)) {
      out.push({ kind: "diff", path: c.path, patch: text.replace(PREVIEW_CUT, "\n\\ $1\n") });
    } else if (c.type === "diff") {
      if (text.trim()) out.push({ kind: "text", text: text.trim(), path: c.path });
    } else if (c.type === "text" && text.trim()) {
      const last = out[out.length - 1];
      if (last?.kind === "text" && !last.path) last.text = `${last.text}\n${text.trim()}`;
      else out.push({ kind: "text", text: text.trim() });
    }
  }
  return out;
}

function PathLabel({ path }: { path: string }) {
  return (
    <div title={path} className="bg-muted/60 text-muted-foreground truncate border-b px-2 py-1 font-mono text-[11px]">
      {path}
    </div>
  );
}

const PRE =
  "scroll-thin bg-muted/60 max-h-80 overflow-auto overscroll-contain p-2 font-mono text-[11px] leading-relaxed break-words whitespace-pre-wrap";

function ContentBlock({ block }: { block: Block }) {
  if (block.kind === "diff") {
    return (
      <div className="overflow-hidden rounded-md border">
        {block.path && <PathLabel path={block.path} />}
        <div className="scroll-thin max-h-80 overflow-auto overscroll-contain">
          <Suspense fallback={null}>
            <ToolDiff patch={block.patch} />
          </Suspense>
        </div>
      </div>
    );
  }
  if (block.path) {
    return (
      <div className="overflow-hidden rounded-md border">
        <PathLabel path={block.path} />
        <pre className={PRE}>{block.text}</pre>
      </div>
    );
  }
  return <pre className={cn(PRE, "rounded-md")}>{block.text}</pre>;
}

export function ToolCard({ threadId, item }: { threadId: string; item: Item }) {
  const [open, setOpen] = useState(false);
  const { setFull, content, input, truncated } = useToolOutput(item);
  const body = useMemo(() => blocks(content), [content]);
  const images = useMemo(() => toolImages([item]), [item]);
  const Icon = TOOL_ICON[item.toolKind ?? "other"] ?? CircleIcon;

  // What opening the card would show, said on the closed card: how much text,
  // and for an edit, how much it changed.
  const { lines, added, removed } = useMemo(() => {
    let lines = 0;
    let added = 0;
    let removed = 0;
    for (const b of body) {
      if (b.kind === "text") lines += b.text.split("\n").length;
      else
        for (const l of parsePatch(b.patch)) {
          if (l.kind === "add") added++;
          else if (l.kind === "del") removed++;
        }
    }
    return { lines, added, removed };
  }, [body]);

  return (
    <div className="fade-in bg-card/60 rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-11 w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0"
      >
        <Icon className="text-muted-foreground size-3.5 shrink-0" />
        <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[13px]">
          {item.title || "tool"}
        </span>
        {/* A shield alone reads as nothing much; the word says it never ran. */}
        {item.status === "declined" && (
          <span className="text-muted-foreground shrink-0 font-mono text-[10px]">declined</span>
        )}
        <StatusMark status={item.status as ToolStatus | undefined} />
        {/* A cut input alone still has a body worth opening for. A preview's
            counts are the preview's, not the output's, so it says neither. */}
        {(body.length > 0 || truncated) && (
          <span className="text-muted-foreground flex shrink-0 items-center gap-1 font-mono text-[10px]">
            {open ? (
              "hide"
            ) : truncated ? (
              "truncated"
            ) : (
              <>
                {(added > 0 || removed > 0) && (
                  <>
                    <span className="text-success">+{added}</span>
                    <span className="text-destructive">−{removed}</span>
                  </>
                )}
                {lines > 0 && <span>{lines} {lines === 1 ? "line" : "lines"}</span>}
              </>
            )}
            <ChevronDownIcon className={cn("size-3 transition-transform", open && "rotate-180")} />
          </span>
        )}
      </button>

      {/* Outside the expander: the point of a tail is that it shows without
          a tap. It only exists while the call runs, so a finished card is
          unchanged. */}
      {running(item) && <LiveTail tail={item.tail} className="mx-3 mb-2" />}

      {/* A screenshot is the point of the call: its thumbnail shows on the
          closed card, not behind a tap. */}
      <ToolImages images={images} className="px-2.5 pb-2" />

      {open && (
        <div className="space-y-2 border-t px-3 py-2">
          {input != null && (
            <pre className="scroll-thin bg-muted/60 text-muted-foreground max-h-40 overflow-auto overscroll-contain rounded-md p-2 font-mono text-[11px] leading-relaxed break-words whitespace-pre-wrap">
              {JSON.stringify(input, null, 2)}
            </pre>
          )}
          {body.map((block, i) => (
            <ContentBlock key={i} block={block} />
          ))}
          {truncated && (
            <Suspense fallback={<Spinner className="text-primary size-3" />}>
              {/* Keyed on the output it was asked over: more landing mid-request
                  remounts it, dropping a body that predates the new output. */}
              <LoadFullOutput
                key={item.content?.length ?? 0}
                threadId={threadId}
                itemId={item.id}
                onLoad={setFull}
              />
            </Suspense>
          )}
        </div>
      )}
    </div>
  );
}

// The expanded body of a run or a fold: the cards and text a summary row
// stands in for. Everything renders the way it would in the open — the same
// content shown two different ways in one transcript is a seam the reader has
// to notice.
function ExpandedItems({ threadId, items }: { threadId: string; items: Item[] }) {
  return (
    <div className="mt-2 space-y-2 border-l pl-3">
      {items.map((item) =>
        item.kind === "tool" ? (
          <ToolCard key={item.id} threadId={threadId} item={item} />
        ) : (
          <Markdown
            key={item.id}
            text={item.text ?? ""}
            className={cn(
              "text-[13px] leading-relaxed break-words",
              item.contentKind === "thought" ? "text-thought italic" : "text-muted-foreground",
            )}
          />
        ),
      )}
    </div>
  );
}

// One unbroken run of tool calls in the turn that is running, as a single
// line. Live, it names the call happening right now and updates in place;
// once the narration has moved past it, it becomes a summary of what ran.
// Either way it expands to the cards, and either way the row never leaves —
// the fix this design carries: nothing at the tail of a working transcript
// disappears out from under the reader.
export function ToolRun({ threadId, items, live }: { threadId: string; items: Item[]; live: boolean }) {
  const [open, setOpen] = useState(false);
  const active = items.filter(running).pop() ?? items[items.length - 1];
  const failed = items.some((i) => i.status === "failed");
  const declined = items.some((i) => i.status === "declined");
  const kinds = Array.from(new Set(items.map((i) => i.toolKind ?? "other")));
  const ActiveIcon = TOOL_ICON[active?.toolKind ?? "other"] ?? CircleIcon;
  const images = useMemo(() => toolImages(items), [items]);

  return (
    <div className="fade-in">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="hover:bg-accent/40 focus-visible:ring-ring text-muted-foreground flex min-h-11 w-full items-center gap-2 rounded-lg px-1.5 py-1.5 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0"
      >
        {live ? (
          <>
            <Spinner className="text-primary size-3.5 shrink-0" />
            <ActiveIcon className="size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate font-mono text-[12px]">
              {active?.title || "working"}
            </span>
          </>
        ) : (
          <>
            <span className="flex shrink-0 items-center gap-1">
              {failed && <XIcon aria-label="A call failed" className="text-destructive size-3.5" />}
              {declined && <ShieldXIcon aria-label="A call was declined" className="size-3.5" />}
              {kinds.slice(0, 4).map((k) => {
                const Icon = TOOL_ICON[k] ?? CircleIcon;
                return <Icon key={k} className="size-3.5" />;
              })}
            </span>
            <span className="min-w-0 flex-1 truncate text-[12px]">{summarise(items)}</span>
          </>
        )}
        {/* `leading-none`, or this span is 12px tall (just the chevron) while the
            label is empty and 15px once "2 calls" arrives with the run's second
            call — and the row centres, so the chevron twitches up 1.5px. */}
        <span className="flex shrink-0 items-center gap-1 font-mono text-[10px] leading-none">
          {open ? "hide" : items.length === 1 ? "" : `${items.length} calls`}
          <ChevronDownIcon className={cn("size-3 transition-transform", open && "rotate-180")} />
        </span>
      </button>

      {/* The tail of the call the row names, under it. Opened, the run's own
          card for that call shows the same tail, so this one steps aside
          rather than print it twice. */}
      {live && !open && running(active) && (
        <LiveTail tail={active?.tail} className="mt-1 ml-1.5" />
      )}

      {/* Closed, the run still shows what its calls saw. Open, each card shows
          its own, so the strip would only be a second copy. */}
      {!open && <ToolImages images={images} className="mt-1 px-1" />}
      {open && <ExpandedItems threadId={threadId} items={items} />}
    </div>
  );
}

// A finished turn's work, behind one quiet line. What the reader keeps is the
// prompt above and the answer below; "Worked for 34s" is the receipt for
// everything in between, and opens to all of it — thoughts, calls, failures.
// A piece after the first, when a plan or spawn batch split the turn, says
// what it holds instead: the duration is the whole turn's, and is said once.
export function TurnFold({
  threadId,
  turn,
  items,
  continued,
}: {
  threadId: string;
  turn: Turn;
  items: Item[];
  continued?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const images = useMemo(() => toolImages(items), [items]);

  return (
    <div className="fade-in">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="hover:text-foreground focus-visible:ring-ring text-muted-foreground flex items-center gap-1 rounded-md px-1 py-0.5 text-left text-[13px] transition-colors outline-none focus-visible:ring-2"
      >
        <span>{continued ? summarise(items) || "More work" : foldLabel(turn)}</span>
        <ChevronDownIcon className={cn("size-3.5 transition-transform", !open && "-rotate-90")} />
      </button>

      {/* Screenshots outlive the fold, the way artefacts do: a picture of the
          result is often the most direct receipt for the work. */}
      {!open && <ToolImages images={images} className="mt-1" />}
      {open && <ExpandedItems threadId={threadId} items={items} />}
    </div>
  );
}
