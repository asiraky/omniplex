import {
  ArchiveIcon,
  ArrowRightIcon,
  BrainIcon,
  CheckIcon,
  ChevronDownIcon,
  CircleIcon,
  CopyIcon,
  DownloadIcon,
  FileTextIcon,
  GitMergeIcon,
  PencilIcon,
  SearchIcon,
  TerminalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react";
import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
} from "react";

import { ChangedFiles } from "~/components/ChangedFiles";
import { IconButton } from "~/components/IconButton";
import { Markdown } from "~/components/Markdown";
import { RecentSkills } from "~/components/RecentSkills";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { attachmentUrl } from "~/lib/attachments";
import { useCopy } from "~/lib/clipboard";
import { fmtTokens } from "~/lib/format";
import { cn } from "~/lib/utils";
import type { ComposerItem, Item, PromptImage, PullRequest, SessionState, ToolStatus, Turn } from "~/protocol";
import { saveResume } from "~/resume";
import { buildRows, foldLabel, rowTurnID, summarise } from "~/rows";
import { atBottom, useAutoScroll } from "~/useAutoScroll";
import { useSmoothText } from "~/useSmoothText";

// Room reserved beneath the transcript's tail: the overlay's measured height
// (`--composer-h`, published by App from a ResizeObserver) plus a headroom band
// so the last message rests clear of the composer with air above it rather than
// pinned to its top edge. The 9rem fallback matches the collapsed composer for
// the first frame, before the measurement lands.
const TAIL_RESERVE = "calc(var(--composer-h, 9rem) + 6rem)";
// The tail's room plus whatever extra the scroll hook is asking for to lift a
// just-sent prompt clear of the composer (`--anchor-reserve`, 0 when it is not).
const CONTENT_RESERVE = `calc(${TAIL_RESERVE} + var(--anchor-reserve, 0px))`;

// How long a ready workspace card stays before it collapses on its own: long
// enough to read "Workspace ready" and reach for the disclosure if the output
// is wanted, short enough that it is gone before the first prompt is typed.
const AUTO_DISMISS_MS = 2500;
// Matches the wrapper's transition duration, plus a frame's grace.
const COLLAPSE_MS = 320;

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
  fetch: DownloadIcon,
  other: CircleIcon,
};

function StatusMark({ status }: { status?: ToolStatus }) {
  if (status === "in_progress" || status === "pending")
    return <Spinner className="text-primary size-3.5" />;
  if (status === "failed")
    return <XIcon aria-label="Failed" className="text-destructive size-3.5" />;
  return <CheckIcon aria-label="Done" className="text-success size-3.5" />;
}

function ToolCard({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  const output = (item.content ?? [])
    .map((c) => (c.type === "diff" ? `--- ${c.path}\n${c.text ?? ""}` : (c.text ?? "")))
    .join("\n")
    .trim();
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
        <StatusMark status={item.status} />
        {output && (
          <span className="text-muted-foreground flex shrink-0 items-center gap-1 font-mono text-[10px]">
            {open ? "hide" : `${output.split("\n").length} lines`}
            <ChevronDownIcon className={cn("size-3 transition-transform", open && "rotate-180")} />
          </span>
        )}
      </button>

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
        </div>
      )}
    </div>
  );
}

// The expanded body of a run or a fold: the cards and text a summary row
// stands in for. Everything renders the way it would in the open — the same
// content shown two different ways in one transcript is a seam the reader has
// to notice.
function ExpandedItems({ items }: { items: Item[] }) {
  return (
    <div className="mt-2 space-y-2 border-l pl-3">
      {items.map((item) =>
        item.kind === "tool" ? (
          <ToolCard key={item.id} item={item} />
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
function ToolRun({ items, live }: { items: Item[]; live: boolean }) {
  const [open, setOpen] = useState(false);
  const active =
    items.filter((i) => i.status === "in_progress" || i.status === "pending").pop() ??
    items[items.length - 1];
  const failed = items.some((i) => i.status === "failed");
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
              {kinds.slice(0, 4).map((k) => {
                const Icon = TOOL_ICON[k] ?? CircleIcon;
                return <Icon key={k} className="size-3.5" />;
              })}
            </span>
            <span className="min-w-0 flex-1 truncate text-[12px]">{summarise(items)}</span>
          </>
        )}
        <span className="flex shrink-0 items-center gap-1 font-mono text-[10px]">
          {open ? "hide" : items.length === 1 ? "" : `${items.length} calls`}
          <ChevronDownIcon className={cn("size-3 transition-transform", open && "rotate-180")} />
        </span>
      </button>

      {open && <ExpandedItems items={items} />}
    </div>
  );
}

// A finished turn's work, behind one quiet line. What the reader keeps is the
// prompt above and the answer below; "Worked for 34s" is the receipt for
// everything in between, and opens to all of it — thoughts, calls, failures.
function TurnFold({ turn, items }: { turn: Turn; items: Item[] }) {
  const [open, setOpen] = useState(false);

  return (
    <div className="fade-in">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="hover:text-foreground focus-visible:ring-ring text-muted-foreground flex items-center gap-1 rounded-md px-1 py-0.5 text-left text-[13px] transition-colors outline-none focus-visible:ring-2"
      >
        <span>{foldLabel(turn)}</span>
        <ChevronDownIcon
          className={cn("size-3.5 transition-transform", !open && "-rotate-90")}
        />
      </button>

      {open && <ExpandedItems items={items} />}
    </div>
  );
}

// A compaction boundary, as one quiet centered line in the flow. The harness
// compressed the conversation to reclaim window; the reader mostly needs to
// know it happened and roughly how much it recovered.
function NoticeCard({ item }: { item: Item }) {
  const detail =
    item.preTokens && item.postTokens
      ? `${fmtTokens(item.preTokens)} → ${fmtTokens(item.postTokens)}`
      : "";
  return (
    <div className="fade-in flex justify-center">
      <div className="text-muted-foreground flex items-center gap-1.5 rounded-full border px-3 py-1 text-[12px]">
        <ArchiveIcon className="size-3.5 shrink-0" />
        <span>
          {item.trigger === "manual" ? "Context compacted" : "Context auto-compacted"}
          {detail && ` — ${detail}`}
        </span>
      </div>
    </div>
  );
}

function receivedTime(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/**
 * The footer under a message: when it arrived, and a one-click copy of
 * the raw text. Quiet by design — metadata should not compete with the prose —
 * so it fades in on hover on a desktop and stays small everywhere.
 */
function MessageMeta({ item }: { item: Item }) {
  const { copied, copy } = useCopy();
  const time = receivedTime(item.receivedAt);
  if (!time && !item.text) return null;

  return (
    <div className="text-muted-foreground flex items-center gap-1.5 text-[13px] opacity-100 transition-opacity md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100">
      {time && <span className="font-mono">{time}</span>}
      <button
        type="button"
        onClick={() => void copy(item.text ?? "")}
        aria-label="Copy message"
        className="hover:text-foreground focus-visible:ring-ring flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 outline-none focus-visible:ring-2"
      >
        {copied ? (
          <CheckIcon className="text-success size-3.5" />
        ) : (
          <CopyIcon className="size-3.5" />
        )}
        {copied ? "copied" : "copy"}
      </button>
    </div>
  );
}

// A user message longer than this many lines collapses behind a fade until the
// reader opens it. The clamp is a real height, not a character count: a single
// long wrapped paste collapses just as a hundred hard breaks would, and a short
// message is judged by how tall it actually renders — so nothing that already
// fits ever grows a button.
const MAX_COLLAPSED_USER_MESSAGE_LINES = 12;
// The fade eats the last line or so, enough to read as "there's more" without
// swallowing a whole line of text.
const COLLAPSED_USER_MESSAGE_FADE = "1.75rem";
const COLLAPSED_USER_MESSAGE_MASK = `linear-gradient(to bottom, black calc(100% - ${COLLAPSED_USER_MESSAGE_FADE}), transparent)`;

// The user's own prompt, which — unlike everything else in the transcript — can
// be an arbitrarily large paste. Left alone it renders at full height forever
// and buries the conversation under the reader's own text, so past the clamp we
// hide the overflow behind a soft fade and a toggle. The fade is a CSS mask, not
// an overlay: it needs no knowledge of the bubble's colour, so it works in both
// themes for free. Expanded state is per-message and never persisted —
// reopening the session starts collapsed again.
// The pictures a prompt carried. Read from the attachment endpoint rather than
// from anything the event carried, so a phone attaching to a session it was not
// in the room for sees exactly what was sent. Each thumbnail is also a link: a
// screenshot cropped to a tile is a reminder of what was sent, not a look at it.
function PromptImages({ sessionId, images }: { sessionId: string; images: PromptImage[] }) {
  return (
    <div className="mb-1.5 flex max-w-[85%] flex-wrap justify-end gap-1.5">
      {images.map((image) => (
        <a
          key={image.id}
          href={attachmentUrl(sessionId, image.id)}
          target="_blank"
          rel="noreferrer"
          className="focus-visible:ring-ring rounded-lg outline-none focus-visible:ring-2"
        >
          <img
            src={attachmentUrl(sessionId, image.id)}
            alt="Attached image"
            loading="lazy"
            className="max-h-36 max-w-[9rem] rounded-lg border object-cover"
          />
        </a>
      ))}
    </div>
  );
}

function UserMessage({ item, sessionId }: { item: Item; sessionId: string }) {
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);

  // Measure the overflow rather than counting characters: the same text is a
  // very different height depending on how it wraps. The clamp is applied
  // whenever the bubble isn't expanded (see `clamped` below), so while
  // collapsed a scrollHeight past the clamp is the real signal there's more.
  // Measuring against the clamped element is what makes this work — measure an
  // unconstrained element and its scrollHeight and clientHeight always agree,
  // so nothing would ever look overflowing. Skip the measure while expanded:
  // the clamp is off, the two heights agree, and re-measuring would only
  // wrongly clear the toggle.
  useLayoutEffect(() => {
    if (expanded) return;
    const el = bodyRef.current;
    if (!el) return;
    const measure = () => setOverflowing(el.scrollHeight - el.clientHeight > 1);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [item.text, expanded]);

  // Clamp whenever not expanded — including before the first measurement — so
  // the measurement above runs against a constrained element. A short message
  // is shorter than the clamp, so the cap does nothing visible to it; only a
  // message that actually overflows gets the fade and the toggle.
  const clamped = !expanded;

  const toggle = () => {
    // Collapsing can leave the bubble's top scrolled off above the viewport;
    // bring it back so the transcript doesn't land somewhere random.
    if (expanded) requestAnimationFrame(() => wrapRef.current?.scrollIntoView({ block: "nearest" }));
    setExpanded((v) => !v);
  };

  return (
    <div ref={wrapRef} data-msg-id={item.id} className="group fade-in flex flex-col items-end">
      {item.images && item.images.length > 0 && <PromptImages sessionId={sessionId} images={item.images} />}
      {/* An image-only message has no bubble to draw: an empty one reads as a
          message that failed to arrive. */}
      {(item.text || !item.images?.length) && (
        <div
          ref={bodyRef}
          style={
            clamped
              ? {
                  maxHeight: `${MAX_COLLAPSED_USER_MESSAGE_LINES}lh`,
                  ...(overflowing && {
                    WebkitMaskImage: COLLAPSED_USER_MESSAGE_MASK,
                    maskImage: COLLAPSED_USER_MESSAGE_MASK,
                  }),
                }
              : undefined
          }
          className={cn(
            "bg-user-bubble text-user-bubble-foreground max-w-[85%] rounded-2xl rounded-br-md px-3.5 py-2 text-[14px] leading-relaxed break-words whitespace-pre-wrap",
            clamped && "overflow-hidden",
          )}
        >
          {item.text}
        </div>
      )}
      {overflowing && (
        <button
          type="button"
          onClick={toggle}
          aria-expanded={expanded}
          className="text-muted-foreground hover:text-foreground focus-visible:ring-ring mt-1 rounded-sm px-1 text-[12px] transition-colors outline-none focus-visible:ring-2"
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
      <MessageMeta item={item} />
    </div>
  );
}

function Message({
  item,
  sessionId,
  streaming,
  recovered,
}: {
  item: Item;
  sessionId: string;
  streaming: boolean;
  recovered?: "restart" | "continue";
}) {
  // Paced reveal, so a harness that delivers a line at a time still reads as
  // continuous output. Inactive messages render whole.
  const text = useSmoothText(item.text ?? "", streaming);

  // The prompt that picks interrupted work back up was written by the server,
  // not by the person reading this. Showing it as their own message would be a
  // lie; so would saying the server restarted when it did not — the same
  // prompt goes out when a human continues a turn that simply failed.
  if (recovered && item.role === "user") {
    return (
      <div className="fade-in flex justify-center">
        <div className="text-muted-foreground rounded-full border px-3 py-1 text-[12px]">
          {recovered === "restart"
            ? "Server restarted — the agent was asked to pick the work back up"
            : "Asked the agent to pick the work back up"}
        </div>
      </div>
    );
  }

  if (item.role === "user") {
    return <UserMessage item={item} sessionId={sessionId} />;
  }

  if (item.contentKind === "thought") {
    return (
      <Markdown
        text={text}
        className="fade-in text-thought border-l-2 pl-3 text-[13px] leading-relaxed break-words italic"
      />
    );
  }

  return (
    <div className="group flex flex-col gap-2">
      <Markdown
        text={text}
        className={cn(
          "fade-in text-[14px] leading-relaxed break-words",
          // The caret belongs at the end of the prose, not below it, so it
          // hangs off the last block rather than the message container.
          streaming && "caret-block",
        )}
      />
      {/* The footer arrives with the message's end: while streaming, the time
          would claim an arrival that has not happened yet. */}
      {!streaming && <MessageMeta item={item} />}
    </div>
  );
}

function WorkspaceCard({
  state,
  onRetry,
  onCleanup,
  onForceDelete,
}: {
  state: SessionState;
  onRetry: () => void;
  onCleanup: () => void;
  onForceDelete: () => void;
}) {
  const ws = state.workspace;
  const active =
    state.phase === "provisioning" || state.phase === "creating" || state.phase === "cleaning";
  const failed = state.phase === "provision_failed" || state.phase === "cleanup_failed";
  const [open, setOpen] = useState(active || failed);
  // A receipt is for the reader who watched the work. A workspace that was
  // already ready when this transcript mounted — every reopen of an old
  // session — has nothing to report, so the card never appears: mounting it
  // only to auto-dismiss it 2.5s later would play its collapse above a
  // transcript pinned to the tail, and the tail wobbles as the scroller
  // chases the shrinking content a frame behind.
  const [dismissed, setDismissed] = useState(ws.phase === "ready" && !active && !failed);
  // The card's exit, in two steps: `leaving` starts the collapse, `dismissed`
  // unmounts it once the collapse has played. A hard unmount would make the
  // rest of the transcript jump.
  const [leaving, setLeaving] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (active || failed) {
      setOpen(true);
      setDismissed(false);
      setLeaving(false);
    } else if (ws.phase === "ready") setOpen(false);
  }, [active, failed, ws.phase]);

  // A finished provisioner is a receipt, not a task: it says its piece and
  // then gets out of the way, rather than holding the top of an empty
  // transcript until someone clicks the X. Only a *ready* workspace leaves —
  // a failed one is asking for a decision and must keep asking. So does an
  // expanded one: the reader opened it to look at the output, and yanking it
  // mid-read would be the same rudeness in the other direction.
  useEffect(() => {
    if (ws.phase !== "ready" || active || failed || open || dismissed) return;
    const timer = setTimeout(() => setLeaving(true), AUTO_DISMISS_MS);
    return () => clearTimeout(timer);
  }, [ws.phase, active, failed, open, dismissed]);

  // Collapse from the height it actually has: an animation from a guessed
  // max-height either clips the card or spends most of its duration playing
  // nothing. Two frames because the browser has to observe the start value
  // before the end value can transition from it.
  useEffect(() => {
    if (!leaving) return;
    const el = wrapRef.current;
    if (!el) {
      setDismissed(true);
      return;
    }
    el.style.maxHeight = `${el.scrollHeight}px`;
    let inner = 0;
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => {
        el.style.maxHeight = "0px";
        el.style.opacity = "0";
      });
    });
    const timer = setTimeout(() => setDismissed(true), COLLAPSE_MS);
    return () => {
      cancelAnimationFrame(outer);
      cancelAnimationFrame(inner);
      clearTimeout(timer);
      // A workspace that goes active again mid-collapse cancels the exit, and
      // the card has to come back at full height — the inline styles the
      // collapse wrote would otherwise leave it flat and invisible, taking any
      // failure controls with it.
      el.style.maxHeight = "";
      el.style.opacity = "";
    };
  }, [leaving]);

  if (!ws.phase || dismissed) return null;

  const title =
    state.phase === "cleaning"
      ? "Cleaning up workspace"
      : failed
        ? "Workspace needs attention"
        : ws.phase === "ready"
          ? "Workspace ready"
          : ws.phase === "released"
            ? "Workspace released"
            : "Preparing workspace";
  const elapsed = ws.durationMs ? `${Math.max(1, Math.round(ws.durationMs / 1000))}s` : "";

  return (
    <div
      ref={wrapRef}
      aria-hidden={leaving || undefined}
      className="motion-reduce:transition-none overflow-hidden transition-[max-height,opacity] duration-300 ease-in"
    >
      <div
        className={cn(
          "fade-in bg-card/70 rounded-xl border",
          failed && "border-destructive/40 bg-destructive/5",
        )}
      >
      <div className="flex items-center gap-2.5 px-3 py-2.5">
        {active ? (
          <Spinner className="text-primary size-4" />
        ) : failed ? (
          <TriangleAlertIcon aria-hidden className="text-destructive size-4 shrink-0" />
        ) : (
          <CheckIcon aria-hidden className="text-success size-4 shrink-0" />
        )}
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="focus-visible:ring-ring min-h-11 min-w-0 flex-1 rounded-sm text-left outline-none focus-visible:ring-2 md:min-h-0"
        >
          <span className="text-muted-foreground block text-[11px]">Workspace provisioner</span>
          <span className="block text-[13px]">
            {title}
            {elapsed && ` · ${elapsed}`}
          </span>
        </button>
        {ws.phase === "ready" && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Dismiss workspace activity"
            className="size-11 md:size-8"
            onClick={() => setLeaving(true)}
          >
            <XIcon />
          </Button>
        )}
      </div>

      {open && (
        <div className="border-t p-3">
          {ws.command && (
            <p className="text-muted-foreground mb-2 truncate font-mono text-[11px]">{ws.command}</p>
          )}
          <pre className="scroll-thin bg-muted/60 max-h-80 min-h-20 overflow-auto rounded-md p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
            {ws.output || (active ? "Starting…" : "No output")}
          </pre>
          {ws.error && (
            <p className="bg-destructive/10 text-destructive mt-2 rounded-md px-2 py-1.5 font-mono text-[11px]">
              {ws.error}
              {ws.exitCode ? ` (exit ${ws.exitCode})` : ""}
            </p>
          )}
          {failed && (
            <div className="mt-3 flex flex-wrap gap-2">
              <Button
                size="sm"
                onClick={state.phase === "cleanup_failed" ? onCleanup : onRetry}
              >
                Retry
              </Button>
              {state.phase === "provision_failed" && (
                <Button size="sm" variant="outline" onClick={onCleanup}>
                  Clean up
                </Button>
              )}
              {state.phase === "cleanup_failed" && (
                <Button size="sm" variant="destructive" onClick={onForceDelete}>
                  Force delete…
                </Button>
              )}
            </div>
          )}
        </div>
      )}
      </div>
    </div>
  );
}

// MergedCard is the whole of the "you are probably done here" affordance: one
// quiet pill at the foot of the transcript, where the reader already is when
// the news arrives. It is not a banner and it does not nag — the transcript is
// the thing being read, so the offer sits in it and is either taken or
// scrolled past. Clicking opens the ordinary delete confirmation, which is
// where the worktree question is actually asked and answered.
function MergedCard({ pr, onFinish }: { pr: PullRequest; onFinish: () => void }) {
  // The tooltip is the explanation, and a touch screen has none — so the
  // label states the fact and the aria-label states the offer, leaving the
  // pill legible without a hover and safe without one too: nothing is
  // destroyed until the confirmation says so.
  const offer = `Pull request #${pr.number} was merged — finish with this session`;
  return (
    <div className="fade-in flex justify-center pt-1 pb-2">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            aria-label={offer}
            onClick={onFinish}
            className="text-muted-foreground hover:text-foreground h-11 rounded-full border border-dashed px-3 text-[12px] font-normal md:h-7"
          >
            <GitMergeIcon aria-hidden className="text-success size-3.5" />
            PR #{pr.number} merged
          </Button>
        </TooltipTrigger>
        <TooltipContent>Done with this session? Delete it and its worktree.</TooltipContent>
      </Tooltip>
    </div>
  );
}

// InterruptedCard is what a turn that died looks like. A cross on the last
// tool call is not an explanation: it says something stopped, not that the
// work is unfinished and nobody is coming back for it. The server retries by
// itself after a restart, so this appears when that did not happen or did not
// work — which is precisely when a human has to decide.
function InterruptedCard({ turn, onContinue }: { turn: Turn; onContinue: () => void }) {
  const [sending, setSending] = useState(false);
  const error = turn.error ?? "";
  // The server says what kind of failure this was; reading the message for it
  // is how every death — a harness that exited, an account that needs to log
  // in again — came to be told as a story about a restart.
  const restarted = turn.failure === "restart";
  // A turn that died for want of a login is not an interruption, and
  // continuing it cannot work: the next attempt fails the same way. What it
  // needs is the one instruction that fixes it.
  const needsLogin = turn.failure === "auth";

  if (needsLogin) {
    return (
      <div className="fade-in border-destructive/30 bg-destructive/5 rounded-lg border px-3.5 py-3">
        <p className="text-[13px]">Claude is not signed in, so this turn could not run.</p>
        <p className="text-muted-foreground mt-1.5 text-[12px]">
          Run <span className="font-mono">claude</span> in a terminal and use{" "}
          <span className="font-mono">/login</span>, or give this provider instance a valid{" "}
          <span className="font-mono">CLAUDE_CODE_OAUTH_TOKEN</span> or{" "}
          <span className="font-mono">ANTHROPIC_API_KEY</span>. Then send the prompt again.
        </p>
      </div>
    );
  }

  return (
    <div className="fade-in border-destructive/30 bg-destructive/5 rounded-lg border px-3.5 py-3">
      <p className="text-[13px]">
        {restarted
          ? "The server restarted and this turn was interrupted before it finished."
          : "This turn ended with an error before it finished."}
      </p>
      {error && !restarted && (
        <p className="text-destructive mt-1.5 font-mono text-[11px] break-words">{error}</p>
      )}
      <p className="text-muted-foreground mt-1.5 text-[12px]">
        {!turn.recovery
          ? "The work was left unfinished."
          : turn.recovery.cause === "continue"
            ? "Continuing it did not work either."
            : "Picking it back up automatically did not work."}
      </p>
      <Button
        size="sm"
        className="mt-2.5"
        disabled={sending}
        onClick={() => {
          setSending(true);
          onContinue();
        }}
      >
        {sending ? "Continuing…" : "Continue where it left off"}
      </Button>
    </div>
  );
}

export function Transcript({
  state,
  initialScroll,
  onScrollChange,
  onRetryProvision,
  onCleanup,
  onForceDelete,
  onContinue,
  onOpenDiff,
  pr,
  onFinish,
  recents = [],
  recentsSeeded = false,
  onPickRecent,
}: {
  state: SessionState;
  /** Where this session was last scrolled — the position the parent kept from
      the previous time this session was open, or the one a resumed page saved
      as it went to background (resume.ts). Applied once, on mount. */
  initialScroll?: { top: number; atBottom: boolean };
  /** Reports where the reader is, so the parent can hand it back the next time
      this session is opened. Switching sessions unmounts this component, so a
      position it kept to itself would die with it. */
  onScrollChange?: (sessionId: string, top: number, atBottom: boolean) => void;
  onRetryProvision: () => void;
  onCleanup: () => void;
  onForceDelete: () => void;
  onContinue: () => void;
  onOpenDiff: (path?: string) => void;
  /** The session branch's pull request, when omniplex could find one. */
  pr?: PullRequest | null;
  /** Opens the delete confirmation for this session. */
  onFinish: () => void;
  /** Skills to offer on an empty transcript, already filtered against this
      session's live catalogue by the parent — never offer what it cannot run. */
  recents?: ComposerItem[];
  /** True when `recents` are catalogue suggestions rather than real history. */
  recentsSeeded?: boolean;
  /** Writes the skill's token into the composer. Omitted, the list is hidden. */
  onPickRecent?: (item: ComposerItem) => void;
}) {
  // The provisioner is holding the transcript while it works, or while it
  // waits for an answer about a failure. Anything else — ready, released, or
  // never provisioned — leaves the empty state to speak.
  const workspaceOccupied =
    state.phase === "creating" ||
    state.phase === "provisioning" ||
    state.phase === "cleaning" ||
    state.phase === "provision_failed" ||
    state.phase === "cleanup_failed";
  // Follow the tail unless the reader has scrolled up; the button below is
  // how they get back. A restore that was scrolled up mounts unpinned, or the
  // first stick would snap it to the bottom over the restored position.
  const { scrollerRef, contentRef, pinned, stick, scrollToBottom, anchorTo } = useAutoScroll<
    HTMLDivElement,
    HTMLDivElement
  >(initialScroll?.atBottom ?? true);

  // The one-shot restore, from the ref because the prop is cleared after
  // mount and later renders must not re-apply a stale position. A restore
  // that was at the tail needs no offset: the pin above starts armed and the
  // stick below lands it, exactly as if the reader had never left.
  const initialScrollRef = useRef(initialScroll);
  useLayoutEffect(() => {
    const init = initialScrollRef.current;
    const el = scrollerRef.current;
    if (!el || !init || init.atBottom) return;
    el.scrollTop = init.top;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useLayoutEffect(stick, [stick, state.items, state.seq]);

  // The other half of resume.ts: as the page goes to background — the moment
  // a mobile browser may discard the tab — save the state and where it was
  // scrolled, so the reload that follows can paint this transcript instead of
  // "Attaching…". The state rides in a ref so the listeners subscribe once
  // rather than per event.
  const latestState = useRef(state);
  useEffect(() => {
    latestState.current = state;
  }, [state]);
  useEffect(() => {
    const save = () => {
      const el = scrollerRef.current;
      if (!el) return;
      saveResume(latestState.current, el.scrollTop, atBottom(el));
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") save();
    };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", save);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", save);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The same idea one scope smaller: where the reader is, reported up as it
  // moves, so that switching to another session and back returns to the place
  // they were reading rather than the bottom. Held in a ref so a new callback
  // identity does not re-subscribe the listener.
  const onScrollChangeRef = useRef(onScrollChange);
  useEffect(() => {
    onScrollChangeRef.current = onScrollChange;
  }, [onScrollChange]);
  // A layout effect, because its cleanup has to run while the scroller is
  // still in the document: a detached node reports a scrollTop of zero, and
  // the final read below is the one that catches movement whose scroll event
  // was still queued when the switch happened.
  useLayoutEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;
    const report = () =>
      onScrollChangeRef.current?.(latestState.current.sessionId, el.scrollTop, atBottom(el));
    el.addEventListener("scroll", report, { passive: true });
    // Mounting counts as a position too: a transcript left at the tail — or
    // one that just restored an offset above — has somewhere to come back to
    // even if the reader never touches it.
    report();
    return () => {
      el.removeEventListener("scroll", report);
      report();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Only the final agent block is still growing; everything above it is
  // settled and renders in full. A block the harness opened but never filled is
  // not it: nothing of it is on screen, so treating it as the growing one would
  // leave the turn looking idle while the agent works. And a block whose turn
  // has finished is not it either — the newest text being settled means
  // nothing is streaming, however the phase got confused.
  // Work done inside a subagent narrates in the subagents surface, not here:
  // interleaving three agents' tool calls into one column reads as noise.
  const ownItems = useMemo(() => state.items.filter((it) => !it.parentId), [state.items]);

  const liveAgentId = useMemo(() => {
    for (let i = ownItems.length - 1; i >= 0; i--) {
      const it = ownItems[i];
      if (it.kind !== "message" || it.role !== "agent" || (it.text ?? "").trim() === "") continue;
      const turn = it.turnId ? state.turns.find((t) => t.id === it.turnId) : undefined;
      return turn?.done ? undefined : it.id;
    }
    return undefined;
  }, [ownItems, state.turns]);

  // Sending a prompt should not leave it jammed against the composer with the
  // answer arriving in the sliver below. The newest prompt is lifted to the top
  // of the view instead, so the reply streams into the open space under it and
  // its first lines are readable the moment they land.
  const lastPromptID = useMemo(() => {
    for (let i = ownItems.length - 1; i >= 0; i--) {
      const it = ownItems[i];
      if (it.kind === "message" && it.role === "user") return it.id;
    }
    return undefined;
  }, [ownItems]);

  // Only a prompt that arrived while this session was already on screen is one
  // the reader just sent. Opening a session, or switching between two, also
  // changes the newest prompt — the whole transcript arrives at once — and
  // yanking the view then would be moving a transcript nobody asked to move.
  // Which is why the session is remembered separately from the prompt: a
  // session first seen with no prompt in it at all still anchors the first one
  // it gets.
  const seenSession = useRef<string>(undefined);
  const seenPrompt = useRef<string>(undefined);
  useLayoutEffect(() => {
    const fresh = seenSession.current !== state.sessionId;
    const prev = seenPrompt.current;
    seenSession.current = state.sessionId;
    seenPrompt.current = lastPromptID;
    if (fresh || !lastPromptID || lastPromptID === prev) return;
    anchorTo(
      scrollerRef.current?.querySelector<HTMLElement>(`[data-msg-id="${lastPromptID}"]`) ?? null,
    );
  }, [state.sessionId, lastPromptID, anchorTo, scrollerRef]);

  const rows = useMemo(
    () => buildRows(ownItems, state.turns, state.phase),
    [ownItems, state.turns, state.phase],
  );

  // Only the newest turn can be continued, and only once nothing is running:
  // an error further back was already answered by whatever came after it.
  const interrupted = useMemo(() => {
    if (state.closed || state.phase === "turn") return undefined;
    const last = state.turns[state.turns.length - 1];
    return last?.done && last.stopReason === "error" ? last : undefined;
  }, [state.turns, state.phase, state.closed]);

  // What each turn changed, to be shown under the turn that changed it. A turn
  // that changed nothing has no entry, and gets no card.
  const turnDiffs = useMemo(
    () => new Map(state.turns.filter((t) => t.diff).map((t) => [t.id, t.diff!])),
    [state.turns],
  );
  const lastTurnID = state.turns[state.turns.length - 1]?.id;

  // Keyed by cause, not merely by "this was recovered": the note the reader
  // gets is a claim about what happened to their work, and only a restart is
  // allowed to claim one.
  const recoveredTurns = useMemo(
    () =>
      new Map(
        state.turns
          .filter((t) => t.recovery)
          .map((t) => [t.id, t.recovery!.cause ?? "restart"] as const),
      ),
    [state.turns],
  );

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={scrollerRef}
        className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain"
        // So a programmatic scrollIntoView lands the target above the floating
        // composer rather than behind it, matching the padding below.
        style={{ scrollPaddingBottom: TAIL_RESERVE }}
      >
        {/* The floating composer overlays the tail, so the content reserves
            real room below it — its measured height plus headroom — and grows
            that room as the composer does, so the tail can always scroll clear
            and rests with breathing space rather than jammed against the input. */}
        <div
          ref={contentRef}
          className="mx-auto flex max-w-3xl flex-col gap-3.5 px-4 pt-6 md:px-5"
          style={{ paddingBottom: CONTENT_RESERVE }}
        >
          <WorkspaceCard
            state={state}
            onRetry={onRetryProvision}
            onCleanup={onCleanup}
            onForceDelete={onForceDelete}
          />
          {/* The empty state used to hide behind any workspace phase at all,
              which left a dismissed-but-ready workspace showing nothing
              whatever. It only needs to stand aside while the provisioner is
              still working or is asking for a decision — once the workspace is
              ready, an empty transcript is an empty transcript. */}
          {state.items.length === 0 && !workspaceOccupied && (
            <div className="text-muted-foreground flex flex-col items-center gap-2 py-20 text-center">
              <TerminalIcon className="size-5 opacity-60" />
              <p className="text-sm">Nothing yet.</p>
              <p className="text-[13px]">Send a prompt to start the turn.</p>
              {recents.length > 0 && onPickRecent && (
                <div className="mt-6 w-full">
                  <RecentSkills items={recents} seeded={recentsSeeded} onPick={onPickRecent} />
                </div>
              )}
            </div>
          )}

          {rows.map((row, i) => {
            const key = row.kind === "item" ? row.item.id : row.id;
            // The card goes after the last row of the turn it describes, which is
            // the row whose successor belongs to a different turn.
            const turnID = rowTurnID(row);
            const nextTurnID = i + 1 < rows.length ? rowTurnID(rows[i + 1]) : undefined;
            const diff = turnID && turnID !== nextTurnID ? turnDiffs.get(turnID) : undefined;

            return (
              // A plain fragment, not a `content-visibility: auto` box. Skipping
              // the render of off-screen rows costs nothing to measure and a lot
              // to scroll: a row that has never been on screen is laid out at
              // its `contain-intrinsic-size` guess, and rows here are anything
              // from a one-line tool call to a screenful of markdown, so every
              // one the reader scrolls up into swaps a 120px placeholder for its
              // real height and shoves the view. That is the scroll-up stutter,
              // and it healed only on the way back down because by then each row
              // had been rendered once and its size remembered.
              <Fragment key={key}>
                {row.kind === "fold" ? (
                  <TurnFold turn={row.turn} items={row.items} />
                ) : row.kind === "run" ? (
                  <ToolRun items={row.items} live={row.live} />
                ) : row.item.kind === "tool" ? (
                  <ToolCard item={row.item} />
                ) : row.item.kind === "notice" ? (
                  <NoticeCard item={row.item} />
                ) : (
                  <Message
                    item={row.item}
                    sessionId={state.sessionId}
                    streaming={state.phase === "turn" && row.item.id === liveAgentId}
                    recovered={
                      (row.item.turnId && recoveredTurns.get(row.item.turnId)) || undefined
                    }
                  />
                )}
                {diff && (
                  <ChangedFiles diff={diff} latest={turnID === lastTurnID} onOpenDiff={onOpenDiff} />
                )}
              </Fragment>
            );
          })}

          {state.phase === "turn" &&
            liveAgentId === undefined &&
            !rows.some((r) => r.kind === "run" && r.live) && (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Spinner className="text-primary size-3.5" /> thinking…
              </div>
            )}

          {interrupted && <InterruptedCard turn={interrupted} onContinue={onContinue} />}

          {/* Last, because it is the latest news about the work above it. */}
          {pr?.merged && <MergedCard pr={pr} onFinish={onFinish} />}
        </div>
      </div>

      {/* Anchored to the scroller rather than to the content, so it sits in
          the same place wherever the transcript happens to be scrolled. It
          rides just above the composer, tracking its measured height so the two
          never overlap however tall the composer grows. The wrapper is inert to
          the pointer: only the button itself may take a click, or a strip of
          dead space would run across the transcript. */}
      {!pinned && (
        <div
          className="pointer-events-none absolute inset-x-0 flex justify-center"
          style={{ bottom: "calc(var(--composer-h, 9rem) + 0.75rem)" }}
        >
          <IconButton
            label="Scroll to bottom"
            variant="outline"
            onClick={scrollToBottom}
            className="fade-in bg-background pointer-events-auto rounded-full shadow-md"
          >
            <ChevronDownIcon />
          </IconButton>
        </div>
      )}
    </div>
  );
}
