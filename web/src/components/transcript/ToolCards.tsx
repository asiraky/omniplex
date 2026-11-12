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
import { useState, type ComponentType } from "react";

import { Markdown } from "~/components/Markdown";
import { Spinner } from "~/components/ui/spinner";
import { cn } from "~/lib/utils";
import type { Item, ToolContent, ToolStatus, Turn } from "~/protocol";
import { foldLabel, summarise } from "~/rows";

import { LiveTail } from "./LiveTail";
import { useFullToolContent, type FullContent } from "./useFullToolContent";

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

function joinOutput(content: ToolContent[] | undefined) {
  return (content ?? [])
    .map((c) => (c.type === "diff" ? `--- ${c.path}\n${c.text ?? ""}` : (c.text ?? "")))
    .join("\n")
    .trim();
}

export function ToolCard({ threadId, item }: { threadId: string; item: Item }) {
  const [open, setOpen] = useState(false);
  const full = useFullToolContent(threadId, item.id);
  // The full output, once fetched, stands in for the preview. It is held by
  // the hook only, so it goes when the card does.
  const shown = full.status === "ready";
  const output = joinOutput(shown ? full.content : item.content);
  const truncated = item.truncated && !shown;
  const Icon = TOOL_ICON[item.toolKind ?? "other"] ?? CircleIcon;

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
        {output && (
          <span className="text-muted-foreground flex shrink-0 items-center gap-1 font-mono text-[10px]">
            {/* A preview's line count is the preview's, not the output's. */}
            {open ? "hide" : truncated ? "truncated" : `${output.split("\n").length} lines`}
            <ChevronDownIcon className={cn("size-3 transition-transform", open && "rotate-180")} />
          </span>
        )}
      </button>

      {/* Outside the expander: the point of a tail is that it shows without
          a tap. It only exists while the call runs, so a finished card is
          unchanged. */}
      {running(item) && <LiveTail tail={item.tail} className="mx-3 mb-2" />}

      {open && (
        <div className="space-y-2 border-t px-3 py-2">
          {item.input != null && (
            <pre className="scroll-thin bg-muted/60 text-muted-foreground max-h-40 overflow-auto overscroll-contain rounded-md p-2 font-mono text-[11px] leading-relaxed">
              {JSON.stringify(item.input, null, 2)}
            </pre>
          )}
          {output && (
            <pre className="scroll-thin bg-muted/60 max-h-80 overflow-auto overscroll-contain rounded-md p-2 font-mono text-[11px] leading-relaxed break-words whitespace-pre-wrap">
              {output}
            </pre>
          )}
          {truncated && <LoadFullOutput full={full} />}
        </div>
      )}
    </div>
  );
}

const textButton =
  "text-muted-foreground hover:text-foreground focus-visible:ring-ring flex min-h-11 items-center gap-1.5 rounded-sm px-1 text-[12px] transition-colors outline-none focus-visible:ring-2 disabled:pointer-events-none md:min-h-0";

/** The way from a truncated preview to the whole output, and back after 4G drops it. */
function LoadFullOutput({ full }: { full: FullContent & { load: () => void } }) {
  if (full.status === "error") {
    return (
      <div className="flex flex-wrap items-center gap-x-2">
        <span role="alert" className="text-destructive text-[12px]">
          {full.error}
        </span>
        <button type="button" onClick={full.load} className={textButton}>
          Retry
        </button>
      </div>
    );
  }
  const loading = full.status === "loading";
  return (
    <button type="button" onClick={full.load} disabled={loading} className={textButton}>
      {loading && <Spinner className="text-primary size-3" />}
      {loading ? "Loading full output…" : "Load full output"}
    </button>
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

      {open && <ExpandedItems threadId={threadId} items={items} />}
    </div>
  );
}
