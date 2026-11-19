import {
  ArchiveIcon,
  ArrowLeftIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  CornerUpLeftIcon,
  MessageSquareIcon,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { ErrorLine, LoadError, Loading } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Marker } from "~/components/ui/marker";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import type { AgentApi } from "~/lib/agentApi";
import { useAgentNav, type AgentFocus } from "~/lib/agentNav";
import { fmtTokens } from "~/lib/format";
import { toast } from "~/lib/toast";
import { cn, errorText } from "~/lib/utils";
import type { Agent, ArchivedAgentThread, CompactionAttachment, CompactionDetail, CompactionRow } from "~/protocol";
import { useLatest } from "~/useLatest";
import { fmtChars, fmtDay, fmtDayTime, harnessName, logRows } from "./agentText";
import { BodyText, InfoBox } from "./parts";

type Detail = { state: "loading" } | { state: "ok"; detail: CompactionDetail } | { state: "error"; message: string };

interface LogData {
  /** The thread asked for: undefined is the live one. */
  asked?: string;
  compactions: CompactionRow[];
  archived: ArchivedAgentThread[];
}

/**
 * The agent page's Log tab: one row per epoch, newest first, epoch 0 held at
 * the bottom. The list is metadata only; a row's bodies load the first time
 * it opens and are kept until the row changes. An archived thread's
 * log replaces the live one while it is picked.
 */
export function LogTab({ agent, api, focus }: { agent: Agent; api: AgentApi; focus?: AgentFocus }) {
  const nav = useAgentNav();
  const apiRef = useLatest(api);
  const harness = harnessName(agent.harness);

  const [threadId, setThreadId] = useState<string | undefined>(undefined);
  const [data, setData] = useState<LogData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [showAll, setShowAll] = useState(false);
  const [open, setOpen] = useState<Set<number>>(() => new Set());
  const [sections, setSections] = useState<Record<string, boolean>>({});
  const [details, setDetails] = useState<Record<number, Detail>>({});
  const requested = useRef(new Set<number>());
  // Each row as last listed. A row can change after it is first logged
  // (Claude's summary and attachments land at the turn's end), and a body
  // fetched before that is dropped so it loads again.
  const listed = useRef(new Map<number, string>());

  // The live log moves when the live thread compacts, is replaced, or has a
  // row filled in; an archived one never changes.
  const liveKey = threadId ? "" : `${agent.threadId ?? ""}:${agent.epoch}:${agent.logVersion ?? ""}`;
  useEffect(() => {
    let live = true;
    setError(null);
    apiRef.current.log(agent.id, threadId).then(
      (res) => {
        if (!live) return;
        const changed: number[] = [];
        for (const r of res.compactions) {
          const sig = rowSig(r);
          const was = listed.current.get(r.id);
          if (was !== undefined && was !== sig) changed.push(r.id);
          listed.current.set(r.id, sig);
        }
        if (changed.length > 0) {
          for (const id of changed) requested.current.delete(id);
          setDetails((d) => {
            const next = { ...d };
            for (const id of changed) delete next[id];
            return next;
          });
        }
        setData({ asked: threadId, compactions: res.compactions, archived: res.archived });
      },
      (e: unknown) => live && setError(errorText(e)),
    );
    return () => {
      live = false;
    };
  }, [apiRef, agent.id, threadId, liveKey, attempt]);

  const load = useCallback(
    (id: number) => {
      if (requested.current.has(id)) return;
      requested.current.add(id);
      setDetails((d) => ({ ...d, [id]: { state: "loading" } }));
      apiRef.current.compaction(agent.id, id).then(
        (detail) => setDetails((d) => ({ ...d, [id]: { state: "ok", detail } })),
        (e: unknown) => {
          requested.current.delete(id);
          setDetails((d) => ({ ...d, [id]: { state: "error", message: errorText(e) } }));
        },
      );
    },
    [apiRef, agent.id],
  );

  // Every open row has its bodies, or is fetching them: on opening, and again
  // when the row changed under a body already fetched. A failed fetch waits
  // for Retry.
  useEffect(() => {
    for (const id of open) if (!details[id]) load(id);
  }, [open, details, load]);

  function toggle(row: CompactionRow) {
    setOpen((s) => {
      const next = new Set(s);
      if (next.has(row.id)) next.delete(row.id);
      else next.add(row.id);
      return next;
    });
  }

  function pickThread(id: string | undefined) {
    setThreadId(id);
    setShowAll(false);
  }

  // An Inspect link lands here with an epoch, and the thread it was in when
  // that is not the live one: show that thread's log, then open the row,
  // paging back to it if it is older than the first page.
  const focusThread = focus?.thread && focus.thread !== agent.threadId ? focus.thread : undefined;
  const focusKey = focus?.epoch !== undefined ? `${focusThread ?? ""}:${focus.epoch}` : undefined;
  const [seenFocus, setSeenFocus] = useState<string | undefined>(undefined);
  const [pendingEpoch, setPendingEpoch] = useState<number | undefined>(undefined);
  if (focusKey !== seenFocus) {
    setSeenFocus(focusKey);
    if (focus?.epoch !== undefined) {
      pickThread(focusThread);
      setPendingEpoch(focus.epoch);
    }
  }

  const current = data && data.asked === threadId ? data : null;
  const [scrollTo, setScrollTo] = useState<{ id: number } | null>(null);
  if (pendingEpoch !== undefined && current) {
    const row = current.compactions.find((r) => r.epoch === pendingEpoch);
    // Not listed yet: the next refetch may bring it.
    if (row) {
      setPendingEpoch(undefined);
      if (!logRows(current.compactions, false).shown.includes(row) && row.epoch !== 0) setShowAll(true);
      setOpen((s) => new Set(s).add(row.id));
      setScrollTo({ id: row.id });
    }
  }
  const rowEls = useRef(new Map<number, HTMLElement>());
  useEffect(() => {
    if (!scrollTo) return;
    const frame = requestAnimationFrame(() => rowEls.current.get(scrollTo.id)?.scrollIntoView?.({ block: "start" }));
    return () => cancelAnimationFrame(frame);
  }, [scrollTo]);

  const viewing = threadId ? current?.archived.find((t) => t.threadId === threadId) : undefined;

  let body: ReactNode;
  if (error && !current) {
    body = <LoadError message={error} onRetry={() => setAttempt((n) => n + 1)} />;
  } else if (!current) {
    body = <Loading>Loading the log</Loading>;
  } else {
    const { shown, hidden, first } = logRows(current.compactions, showAll);
    const row = (r: CompactionRow) => (
      <LogRow
        key={r.id}
        row={r}
        harness={harness}
        claude={agent.harness === "claude"}
        open={open.has(r.id)}
        onToggle={() => toggle(r)}
        detail={details[r.id]}
        onRetry={() => load(r.id)}
        sections={sections}
        onSection={(key, on) => setSections((s) => ({ ...s, [key]: on }))}
        onRev={(kind, rev) => nav.openAgent(agent.id, kind, { rev })}
        onBefore={r.boundarySeq !== undefined ? () => nav.openThread(r.threadId, r.boundarySeq) : undefined}
        rowRef={(el) => {
          if (el) rowEls.current.set(r.id, el);
          else rowEls.current.delete(r.id);
        }}
      />
    );
    body =
      current.compactions.length === 0 ? (
        <InfoBox>
          {threadId || agent.threadId
            ? "Nothing is logged for this thread yet."
            : "No thread has run yet. The log starts with epoch 0, the first request."}
        </InfoBox>
      ) : (
        <>
          <Summary rows={current.compactions} />
          <p className="text-muted-foreground mb-3 text-[12.5px] leading-snug">
            Each row is what the model had in front of it when that epoch began. Newest first.
          </p>
          <div className="space-y-1.5">
            {shown.map(row)}
            {hidden > 0 && (
              <Button variant="secondary" className="w-full md:h-9" onClick={() => setShowAll(true)}>
                Show {hidden} earlier compaction{hidden === 1 ? "" : "s"}
              </Button>
            )}
            {first && row(first)}
          </div>
        </>
      );
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-5 md:px-8 md:py-6">
      {threadId && (
        <div className="bg-muted/40 mb-4 flex flex-wrap items-center gap-2 rounded-md border px-2 py-1.5">
          <Button variant="ghost" size="sm" className="h-11 md:h-8" onClick={() => pickThread(undefined)}>
            <ArrowLeftIcon className="size-3.5" />
            Live log
          </Button>
          <span className="min-w-0 flex-1 truncate text-[12.5px]">
            <ArchiveIcon className="text-muted-foreground mr-1.5 inline size-3.5 align-[-2px]" aria-hidden />
            {viewing ? archivedLabel(viewing) : "Archived thread"}
          </span>
          <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => nav.openThread(threadId)}>
            <MessageSquareIcon className="size-3.5" />
            Open thread
          </Button>
        </div>
      )}
      {body}
      {current && current.archived.length > 0 && (
        <Archived threads={current.archived} viewing={threadId} onPick={pickThread} onOpen={(id) => nav.openThread(id)} />
      )}
      <RawLog agent={agent} api={api} />
    </div>
  );
}

/** What a listed row says about itself; a change means its bodies changed too. */
function rowSig(r: CompactionRow): string {
  return JSON.stringify([r.summaryChars, r.instructionsChars, r.attachments, r.preTokens, r.postTokens, r.durationMs]);
}

function archivedLabel(t: ArchivedAgentThread): string {
  const span = t.closedAt ? `${fmtDay(t.createdAt)} to ${fmtDay(t.closedAt)}` : fmtDay(t.createdAt);
  return t.title ? `${span} · ${t.title}` : span;
}

/** The figures above the rows: how many, how big on average, the latest summary, when it started. */
function Summary({ rows }: { rows: CompactionRow[] }) {
  const comps = rows.filter((r) => r.epoch > 0).sort((a, b) => b.epoch - a.epoch);
  const first = rows.find((r) => r.epoch === 0);
  const avg = (pick: (r: CompactionRow) => number | undefined) => {
    const xs = comps.map(pick).filter((x): x is number => x !== undefined);
    return xs.length ? xs.reduce((s, x) => s + x, 0) / xs.length : undefined;
  };
  const pre = avg((r) => r.preTokens);
  const post = avg((r) => r.postTokens);
  const latest = comps[0]?.summaryChars;
  const figures: [string, string][] = [[String(comps.length), comps.length === 1 ? "compaction" : "compactions"]];
  if (pre !== undefined || post !== undefined) figures.push([`${tok(pre)} → ${tok(post)}`, "average"]);
  if (latest !== undefined) figures.push([fmtChars(latest), "latest summary, chars"]);
  if (first) figures.push([fmtDay(first.createdAt), "thread started"]);
  return (
    <dl className="mb-3 flex flex-wrap gap-x-8 gap-y-3">
      {figures.map(([v, k]) => (
        <div key={k} className="flex flex-col-reverse">
          <dt className="text-muted-foreground text-[12px]">{k}</dt>
          <dd className="font-mono text-[14px] font-semibold">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function tok(n: number | undefined): string {
  return n === undefined ? "?" : fmtTokens(n);
}

function NotReported({ harness }: { harness: string }) {
  return <p className="text-muted-foreground py-1 text-[12.5px] italic">Not reported by {harness}.</p>;
}

// Where an epoch's starting tokens went, by kind; the summary always first.
const KIND_LABEL: Record<string, string> = {
  summary: "summary",
  file: "re-read files",
  skill: "skills",
  instructions: "instructions",
  skills: "skill list",
  task: "tasks",
};
const PALETTE = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

function StartBar({ epoch, post, summaryChars, attachments }: {
  epoch: number;
  post?: number;
  summaryChars: number;
  attachments: CompactionAttachment[];
}) {
  const parts: [string, number][] = [];
  if (summaryChars) parts.push(["summary", summaryChars]);
  const byKind = new Map<string, number>();
  for (const a of attachments) byKind.set(a.kind, (byKind.get(a.kind) ?? 0) + a.chars);
  for (const [k, n] of byKind) if (n) parts.push([k, n]);
  const total = parts.reduce((s, [, n]) => s + n, 0);
  if (!total) return null;
  const colour = (i: number, k: string) => (k === "summary" ? "var(--primary)" : PALETTE[(i - 1) % PALETTE.length]);
  return (
    <div className="mb-2">
      <p className="text-muted-foreground mb-1 text-[11.5px]">
        What epoch {epoch} started from{post !== undefined ? `, ${fmtTokens(post)} tokens` : ""}
      </p>
      <div className="bg-muted flex h-1.5 overflow-hidden rounded-full" aria-hidden>
        {parts.map(([k, n], i) => (
          <i key={k} style={{ width: `${(n / total) * 100}%`, background: colour(i, k) }} />
        ))}
      </div>
      <div className="text-muted-foreground mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-[11.5px]">
        {parts.map(([k, n], i) => (
          <span key={k} className="inline-flex items-center gap-1">
            <i className="inline-block size-2 rounded-[2px]" style={{ background: colour(i, k) }} />
            {KIND_LABEL[k] ?? k} {fmtChars(n)}
          </span>
        ))}
      </div>
    </div>
  );
}

function LogRow({
  row,
  harness,
  claude,
  open,
  onToggle,
  detail,
  onRetry,
  sections,
  onSection,
  onRev,
  onBefore,
  rowRef,
}: {
  row: CompactionRow;
  harness: string;
  claude: boolean;
  open: boolean;
  onToggle: () => void;
  detail?: Detail;
  onRetry: () => void;
  sections: Record<string, boolean>;
  onSection: (key: string, on: boolean) => void;
  onRev: (kind: "prompt" | "compact", rev: number) => void;
  onBefore?: () => void;
  rowRef: (el: HTMLElement | null) => void;
}) {
  const start = row.epoch === 0;
  // Summary opens by default on a compaction, the system prompt on epoch 0:
  // each is what that row is usually opened to read.
  const section = (name: string, title: string, size: string, children: () => ReactNode, byDefault = false) => {
    const key = `${row.id}:${name}`;
    const on = sections[key] ?? byDefault;
    return (
      <Fold title={title} size={size} open={on} onOpenChange={(o) => onSection(key, o)}>
        {on && children()}
      </Fold>
    );
  };

  let inner: ReactNode = null;
  if (open) {
    const d = detail?.state === "ok" ? detail.detail : undefined;
    const attChars = (d?.attachments ?? row.attachments).reduce((s, a) => s + a.chars, 0);
    const attachments = d ? (
      d.attachments.length === 0 ? (
        <NotReported harness={harness} />
      ) : (
        <>
          {d.attachments.map((a) => (
            <Item
              key={`${a.kind}:${a.name}`}
              kind={a.kind === "file" ? "re-read" : a.kind}
              name={a.name}
              chars={a.chars}
              body={a.body}
              harness={harness}
            />
          ))}
          {claude && !start && d.attachments.some((a) => a.kind === "file") && (
            <p className="text-muted-foreground pt-2 text-[12px] leading-snug">
              Claude Code re-reads its five most recent files after every compaction. That cannot be turned off, so
              they are listed with sizes.
            </p>
          )}
        </>
      )
    ) : null;

    inner = (
      <div className="border-t px-3 pt-2.5 pb-3">
        <div className="text-muted-foreground mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px]">
          <span>{fmtDayTime(row.createdAt)}</span>
          {!start &&
            (row.durationMs !== undefined ? (
              <span>took {Math.max(1, Math.round(row.durationMs / 1000))}s</span>
            ) : (
              <span>duration not reported by {harness}</span>
            ))}
          <span>
            prompt <RevLink onClick={() => onRev("prompt", row.promptRev)}>r{row.promptRev}</RevLink>
          </span>
          <span>
            compaction <RevLink onClick={() => onRev("compact", row.compactRev)}>c{row.compactRev}</RevLink>
          </span>
          {onBefore && (
            <button
              type="button"
              onClick={onBefore}
              className="text-primary inline-flex min-h-11 items-center gap-1 hover:underline md:min-h-0"
            >
              <CornerUpLeftIcon className="size-3" aria-hidden />
              Before: open the thread here
            </button>
          )}
        </div>
        {!start && row.preTokens === undefined && row.postTokens === undefined && (
          <p className="text-muted-foreground mb-2 text-[12px]">Token counts not reported by {harness}.</p>
        )}
        {detail?.state === "loading" && <Loading>Loading</Loading>}
        {detail?.state === "error" && <LoadError message={detail.message} onRetry={onRetry} />}
        {d && !start && (
          <>
            <StartBar
              epoch={row.epoch}
              post={row.postTokens}
              summaryChars={d.summary?.length ?? row.summaryChars ?? 0}
              attachments={d.attachments}
            />
            {section(
              "summary",
              "Summary",
              d.summary ? `${fmtChars(d.summary.length)} chars` : "",
              () => (d.summary ? <BodyText>{d.summary}</BodyText> : <NotReported harness={harness} />),
              true,
            )}
            {section(
              "instructions",
              "Instructions sent",
              `c${row.compactRev}${d.instructions ? ` · ${fmtChars(d.instructions.length)} chars` : ""}`,
              () => (d.instructions ? <BodyText>{d.instructions}</BodyText> : <NotReported harness={harness} />),
            )}
            {section(
              "attachments",
              "Attachments",
              d.attachments.length ? `${d.attachments.length} · ${fmtChars(attChars)} chars` : "",
              () => attachments,
            )}
          </>
        )}
        {d && start && (
          <>
            {section(
              "system",
              "System prompt",
              d.system?.length ? `${fmtChars(d.system.reduce((s, p) => s + p.chars, 0))} chars` : "",
              () =>
                d.system?.length ? (
                  d.system.map((p) => (
                    <Item
                      key={`${p.kind}:${p.name}`}
                      kind={p.kind}
                      name={p.name}
                      chars={p.chars}
                      body={p.body}
                      harness={harness}
                    />
                  ))
                ) : (
                  <p className="text-muted-foreground py-1 text-[12.5px] italic">Not recorded.</p>
                ),
              true,
            )}
            {section(
              "first",
              "First message",
              d.summary ? `${fmtChars(d.summary.length)} chars` : "",
              () => (d.summary ? <BodyText>{d.summary}</BodyText> : <NotReported harness={harness} />),
            )}
            {section(
              "attachments",
              "Loaded at start",
              d.attachments.length ? `${fmtChars(attChars)} chars` : "",
              () => attachments,
            )}
          </>
        )}
      </div>
    );
  }

  return (
    <div ref={rowRef} data-epoch={row.epoch} className="bg-card scroll-mt-4 rounded-lg border">
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-12 w-full items-center gap-2.5 rounded-lg px-3 text-left outline-none focus-visible:ring-2"
      >
        <span className="w-6 shrink-0 font-mono text-[12.5px] font-semibold tabular-nums">{row.epoch}</span>
        <Marker tone={row.trigger === "manual" ? "attention" : "quiet"}>{row.trigger}</Marker>
        {start ? (
          <span className="min-w-0 flex-1 truncate text-[12.5px]">First request</span>
        ) : (
          <span className="min-w-0 flex-1 truncate font-mono text-[12px]">
            {tok(row.preTokens)} → {tok(row.postTokens)}
          </span>
        )}
        <span className={cn("text-muted-foreground font-mono text-[11px] whitespace-nowrap", start && "max-md:hidden")}>
          r{row.promptRev} · c{row.compactRev}
        </span>
        <span className={cn("text-muted-foreground font-mono text-[11px] whitespace-nowrap", !start && "max-md:hidden")}>
          {fmtDay(row.createdAt)}
        </span>
        <ChevronDownIcon
          className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-180")}
          aria-hidden
        />
      </button>
      {inner}
    </div>
  );
}

function RevLink({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="text-primary min-h-11 font-mono hover:underline md:min-h-0"
    >
      {children}
    </button>
  );
}

/** A foldable part of an opened row, its size at the right so it can be judged before opening. */
function Fold({
  title,
  size,
  open,
  onOpenChange,
  children,
}: {
  title: string;
  size: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="border-t first-of-type:border-t-0">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => onOpenChange(!open)}
        className="focus-visible:ring-ring flex min-h-11 w-full items-center gap-2 rounded-md text-left text-[13px] outline-none focus-visible:ring-2 md:min-h-9"
      >
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} aria-hidden />
        <span className="flex-1">{title}</span>
        {size && <span className="text-muted-foreground font-mono text-[11px]">{size}</span>}
      </button>
      {open && <div className="pb-3">{children}</div>}
    </section>
  );
}

/** One attachment or system prompt part: its kind, name and size, and its body a tap away. */
function Item({
  kind,
  name,
  chars,
  body,
  harness,
}: {
  kind: string;
  name: string;
  chars: number;
  body?: string;
  harness: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border-b border-dashed last:border-b-0">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="focus-visible:ring-ring flex min-h-11 w-full items-center gap-3 rounded-md text-left outline-none focus-visible:ring-2 md:min-h-9"
      >
        <span className="text-muted-foreground w-20 shrink-0 truncate font-mono text-[11px]">{kind}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{name}</span>
        <span className="text-muted-foreground font-mono text-[11px]">{fmtChars(chars)}</span>
      </button>
      {open && <div className="pb-2">{body ? <BodyText>{body}</BodyText> : <NotReported harness={harness} />}</div>}
    </div>
  );
}

function Archived({
  threads,
  viewing,
  onPick,
  onOpen,
}: {
  threads: ArchivedAgentThread[];
  viewing?: string;
  onPick: (threadId: string) => void;
  onOpen: (threadId: string) => void;
}) {
  return (
    <section aria-label="Archived threads" className="mt-6">
      <h3 className="text-muted-foreground mb-1.5 text-[12px]">Archived threads</h3>
      <ul className="space-y-1.5">
        {[...threads]
          .sort((a, b) => b.createdAt - a.createdAt)
          .map((t) => (
            <li
              key={t.threadId}
              className={cn("bg-card flex items-center rounded-lg border", viewing === t.threadId && "border-primary/60")}
            >
              <button
                type="button"
                aria-current={viewing === t.threadId || undefined}
                onClick={() => onPick(t.threadId)}
                className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-12 min-w-0 flex-1 items-center gap-2.5 rounded-l-lg px-3 text-left outline-none focus-visible:ring-2"
              >
                <ArchiveIcon className="text-muted-foreground size-3.5 shrink-0" aria-hidden />
                <span className="min-w-0 flex-1 truncate text-[12.5px]">{archivedLabel(t)}</span>
                <span className="text-muted-foreground font-mono text-[11px] whitespace-nowrap">
                  {t.compactions} compaction{t.compactions === 1 ? "" : "s"}
                </span>
                <ChevronRightIcon className="text-muted-foreground size-3.5 shrink-0" aria-hidden />
              </button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Open thread"
                className="mr-0.5 md:size-8"
                onClick={() => onOpen(t.threadId)}
              >
                <MessageSquareIcon className="size-3.5" />
              </Button>
            </li>
          ))}
      </ul>
    </section>
  );
}

/** The debug switch beside the view it exists to check. Read at process start, like compact-at. */
function RawLog({ agent, api }: { agent: Agent; api: AgentApi }) {
  // Claude Code's OTEL_LOG_RAW_API_BODIES is the only one there is.
  const supported = agent.harness === "claude";
  // A flip stands until the saved value catches up with it.
  const [picked, setPicked] = useState<boolean | null>(null);
  const [pickedFor, setPickedFor] = useState(agent.rawLog);
  if (pickedFor !== agent.rawLog) {
    setPickedFor(agent.rawLog);
    setPicked(null);
  }
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const on = picked ?? !!agent.rawLog;

  async function set(next: boolean) {
    setPicked(next);
    setSaving(true);
    setError(null);
    try {
      await api.save(agent.id, { rawLog: next });
      toast.success(`Raw request logging ${next ? "on" : "off"}. Applies at the next start or restart.`);
    } catch (e) {
      setPicked(null);
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <section aria-label="Log raw request bodies" className="mt-8">
      <label className="flex items-start gap-4">
        <span className="min-w-0 flex-1">
          <span className="block text-[13.5px] font-medium">Log raw request bodies</span>
          <span className="text-muted-foreground block text-[12.5px] leading-snug">
            {supported
              ? "Debug. Writes every request with its full context to disk, for when this reconstructed view might not match what was sent. Fills the disk quickly."
              : `${harnessName(agent.harness)} has no raw request log.`}
          </span>
        </span>
        <Switch
          checked={on}
          disabled={!supported || saving}
          onCheckedChange={(v) => void set(v)}
          className="mt-1"
        />
      </label>
      {saving && <Spinner className="mt-1 size-3.5" />}
      {error && <ErrorLine message={error} className="mt-2" />}
    </section>
  );
}
