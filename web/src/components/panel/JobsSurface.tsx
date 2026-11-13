import { ArrowLeftIcon, BotIcon, SquareIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import { Markdown } from "~/components/Markdown";
import { RetryRow, textButton } from "~/components/RetryRow";
import { Elapsed } from "~/components/Elapsed";
import { Spinner } from "~/components/ui/spinner";
import { fmtTokens } from "~/lib/format";
import { childJobs, isLive, jobLabel, jobTree, liveJobsLabel } from "~/lib/jobs";
import { cn } from "~/lib/utils";
import type { Item, Job, JobKind, ThreadState } from "~/protocol";
import { useLatest } from "~/useLatest";

import { mergeChildren, useAgentChildren } from "./useAgentChildren";

export interface JobsSurfaceProps {
  threadId: string;
  /** A ws command: `stop_job` and `thread_job_output`. */
  command: (command: string, args: unknown) => Promise<any>;
  /** Fetch the jobs the snapshot left out (`state.jobsBefore`); null on failure. */
  loadAllJobs?: () => Promise<ThreadState | null>;
  /**
   * A job to open, asked for from outside (a row of the transcript's agents
   * card). Each new nonce opens it again, whatever is on screen.
   */
  focus?: { jobId: string; nonce: number };
}

function StatusMark({ job }: { job: Job }) {
  const s = job.status;
  return (
    <span
      className={cn(
        "mt-1.5 size-2 shrink-0 rounded-full",
        s === "running" && "bg-primary animate-pulse",
        s === "paused" && "bg-amber-400 ring-2 ring-amber-400/40",
        s === "completed" && "bg-success",
        s === "failed" && "bg-destructive",
        (s === "stopped" || s === "interrupted") && "bg-muted-foreground",
      )}
      title={s}
    />
  );
}

function fmtCost(c: number): string {
  return c < 0.01 ? "<$0.01" : `$${c.toFixed(2)}`;
}

function usageParts(job: Job): { key: string; part: string }[] {
  const out: { key: string; part: string }[] = [];
  if (job.usage?.totalTokens) out.push({ key: "tokens", part: `${fmtTokens(job.usage.totalTokens)} tok` });
  if (job.usage?.cost) out.push({ key: "cost", part: fmtCost(job.usage.cost) });
  return out;
}

function JobRow({
  job,
  onOpen,
  onStop,
}: {
  job: Job;
  onOpen: (j: Job) => void;
  onStop: (j: Job) => void;
}) {
  const live = isLive(job);
  const meta: { key: string; part: ReactNode }[] = [
    ...(job.kind === "agent" && job.taskType ? [{ key: "type", part: job.taskType }] : []),
    { key: "elapsed", part: <Elapsed since={job.startedAt} finishedAt={job.finishedAt} /> },
    ...usageParts(job),
  ];
  const second = live ? job.activity || "working…" : job.error || job.activity || job.status;

  return (
    // Fixed height on purpose: a streaming activity line must never reflow the
    // roster under the reader's eyes.
    <div
      className="bg-card/60 flex h-[3.75rem] items-stretch overflow-hidden rounded-lg border"
      style={{ marginLeft: `${Math.min(job.depth, 4) * 0.75}rem` }}
    >
      <button
        type="button"
        onClick={() => onOpen(job)}
        className="hover:bg-accent/40 flex min-w-0 flex-1 items-start gap-2.5 px-3 py-1.5 text-left outline-none"
      >
        <StatusMark job={job} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[12.5px] font-medium">{jobLabel(job)}</p>
          <p className={cn("h-[1.1rem] truncate font-mono text-[11px]", job.error ? "text-destructive" : "text-muted-foreground")}>
            {second}
          </p>
          <p className="text-muted-foreground truncate text-[10px]">
            {meta.map(({ key, part }, i) => (
              <span key={key}>
                {i > 0 && " · "}
                {part}
              </span>
            ))}
          </p>
        </div>
      </button>
      {live && (
        <button
          type="button"
          aria-label={`Stop ${jobLabel(job)}`}
          onClick={() => onStop(job)}
          className="text-muted-foreground hover:text-destructive hover:bg-destructive/10 flex min-w-11 shrink-0 items-center justify-center border-l outline-none"
        >
          <SquareIcon className="size-3.5 fill-current" />
        </button>
      )}
    </div>
  );
}

const SECTIONS: { kind: JobKind; title: string }[] = [
  { kind: "agent", title: "Agents" },
  { kind: "shell", title: "Shells" },
  { kind: "monitor", title: "Monitors" },
];

// ---- drill-downs ----

const TAIL_CAP = 200_000;

/** A live tail of a shell job's output file, polled by offset while it runs. */
function ShellPane({
  threadId,
  job,
  command,
}: {
  threadId: string;
  job: Job;
  command: JobsSurfaceProps["command"];
}) {
  const preRef = useRef<HTMLPreElement>(null);
  const [text, setText] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");

  // The poll must survive re-renders: a restart would re-read from zero and
  // append the file a second time. The command prop is read through a ref so
  // its identity cannot retrigger the effect; the offset lives in a ref so a
  // restarted poll continues where the last one left off. A different job is
  // a fresh pane: the caller keys this by job id.
  const commandRef = useLatest(command);
  const offsetRef = useRef(0);
  useEffect(() => {
    if (!job.outputFile) return;
    let stopped = false;
    let timer = 0;
    const poll = async () => {
      try {
        const res = (await commandRef.current("thread_job_output", {
          threadId,
          jobId: job.id,
          offset: offsetRef.current,
        })) as {
          text: string;
          offset: number;
          done: boolean;
        };
        if (stopped) return;
        offsetRef.current = res.offset;
        if (res.text) {
          setText((t) => {
            const next = t + res.text;
            return next.length > TAIL_CAP ? next.slice(next.length - TAIL_CAP) : next;
          });
        }
        if (res.done) {
          setDone(true);
          return;
        }
      } catch (e) {
        if (stopped) return;
        setError(e instanceof Error ? e.message : String(e));
        return;
      }
      timer = window.setTimeout(() => void poll(), 1500);
    };
    void poll();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [threadId, job.id, job.outputFile, commandRef]);

  useEffect(() => {
    const el = preRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [text]);

  if (!job.outputFile) {
    return (
      <p className="text-muted-foreground px-3 py-4 text-[12px]">This shell has no output file to tail.</p>
    );
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <pre
        ref={preRef}
        className="scroll-thin min-h-0 flex-1 overflow-auto px-3 py-2 font-mono text-[11px] leading-snug whitespace-pre-wrap break-all"
      >
        {text || (done ? "(no output)" : "waiting for output…")}
      </pre>
      <p className="text-muted-foreground truncate border-t px-3 py-1 text-[10px]">
        {error ? `Tail failed: ${error}` : done ? "Finished" : "Tailing…"} · {job.outputFile}
      </p>
    </div>
  );
}

function ToolRowMark({ it }: { it: Item }) {
  const s = it.status;
  return (
    <span
      className={cn(
        "mt-1.5 size-1.5 shrink-0 rounded-full",
        (s === "pending" || s === "in_progress") && "bg-primary animate-pulse",
        s === "completed" && "bg-success",
        s === "failed" && "bg-destructive",
        s === "cancelled" && "bg-muted-foreground",
      )}
    />
  );
}

/** What one agent did: its items, plus its own child agents. */
function AgentPane({
  threadId,
  job,
  jobs,
  items,
  onOpen,
  onStop,
}: {
  threadId: string;
  job: Job;
  jobs: Job[];
  items: Item[];
  onOpen: (j: Job) => void;
  onStop: (j: Job) => void;
}) {
  const live = useMemo(
    () => (job.toolCallId ? items.filter((it) => it.parentId === job.toolCallId) : []),
    [items, job.toolCallId],
  );
  // The snapshot holds only part of an agent's narration, if any: none for a
  // settled one, the tail for a running one. A spawn call outside the window
  // says nothing about how much is missing, so that asks too.
  const spawn = job.toolCallId ? items.find((it) => it.id === job.toolCallId) : undefined;
  const wanted = !!job.toolCallId && (!spawn || (spawn.childrenBefore ?? 0) > 0);
  const fetched = useAgentChildren(threadId, job.toolCallId, wanted);
  const fetchedItems = fetched.status === "ready" ? fetched.items : undefined;
  const mine = useMemo(() => mergeChildren(fetchedItems, live), [fetchedItems, live]);
  const children = useMemo(() => childJobs(jobs, job.id), [jobs, job.id]);

  return (
    <div className="scroll-thin h-full space-y-1.5 overflow-y-auto overscroll-contain p-2">
      {fetched.status === "loading" && (
        <p className="text-muted-foreground flex items-center gap-1.5 px-1 py-1 text-[12px]">
          <Spinner className="text-primary size-3" />
          Loading this agent's activity…
        </p>
      )}
      {fetched.status === "error" && <RetryRow className="px-1" message={fetched.error} onRetry={fetched.retry} />}
      {fetched.status !== "loading" && fetched.status !== "error" && mine.length === 0 && children.length === 0 && (
        <p className="text-muted-foreground px-1 py-4 text-center text-[12px]">Nothing recorded for this agent yet.</p>
      )}
      {mine.map((it) =>
        it.kind === "tool" ? (
          <div key={it.id} className="flex items-start gap-2 px-1 text-[11.5px]">
            <ToolRowMark it={it} />
            <span className="min-w-0 flex-1 truncate font-mono">{it.title || it.toolKind}</span>
          </div>
        ) : it.kind === "message" && it.text ? (
          <div
            key={it.id}
            className={cn(
              "min-w-0 overflow-hidden rounded-md px-2 py-1 text-[12px] break-words [&_pre]:overflow-x-auto",
              it.contentKind === "thought" ? "text-muted-foreground italic" : "bg-card/60 border",
            )}
          >
            <Markdown text={it.text} />
          </div>
        ) : null,
      )}
      {children.length > 0 && (
        <>
          <p className="text-muted-foreground px-1 pt-2 text-[10px] font-semibold tracking-wide uppercase">Subagents</p>
          {children.map((c) => (
            <JobRow key={c.id} job={{ ...c, depth: 0 }} onOpen={onOpen} onStop={onStop} />
          ))}
        </>
      )}
    </div>
  );
}

/** The way to the jobs a snapshot left out, and back after 4G drops the fetch. */
function EarlierJobs({ load }: { load: () => Promise<ThreadState | null> }) {
  const [status, setStatus] = useState<"idle" | "loading" | "error">("idle");
  const run = () => {
    setStatus("loading");
    // Success needs no state of its own: the merged list arrives through
    // onState with jobsBefore cleared, and this row goes with it.
    void load().then((next) => setStatus(next ? "idle" : "error"));
  };
  if (status === "error") return <RetryRow className="px-1" message="Could not load the earlier jobs." onRetry={run} />;
  const loading = status === "loading";
  return (
    <button type="button" onClick={run} disabled={loading} className={textButton}>
      {loading && <Spinner className="text-primary size-3" />}
      {loading ? "Loading earlier jobs…" : "Show earlier jobs"}
    </button>
  );
}

/** One job opened from the roster: its header, and its shell or narration. */
function OpenJob({
  threadId,
  job,
  jobs,
  items,
  command,
  onBack,
  onOpen,
  onStop,
}: {
  threadId: string;
  job: Job;
  jobs: Job[];
  items: Item[];
  command: JobsSurfaceProps["command"];
  onBack: () => void;
  onOpen: (j: Job) => void;
  onStop: (j: Job) => void;
}) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-h-11 items-center gap-1 border-b pr-2">
        <button
          type="button"
          aria-label="Back to jobs"
          onClick={onBack}
          className="hover:bg-accent/50 flex min-h-11 min-w-11 shrink-0 items-center justify-center outline-none"
        >
          <ArrowLeftIcon className="size-4" />
        </button>
        <StatusMark job={job} />
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium">{jobLabel(job)}</span>
        <span className="text-muted-foreground shrink-0 text-[10px]">
          <Elapsed since={job.startedAt} finishedAt={job.finishedAt} />
        </span>
        {isLive(job) && (
          <button
            type="button"
            aria-label={`Stop ${jobLabel(job)}`}
            onClick={() => onStop(job)}
            className="text-muted-foreground hover:text-destructive flex min-h-11 min-w-11 items-center justify-center outline-none"
          >
            <SquareIcon className="size-3.5 fill-current" />
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1">
        {job.kind === "shell" ? (
          <ShellPane key={job.id} threadId={threadId} job={job} command={command} />
        ) : (
          <AgentPane
            threadId={threadId}
            job={job}
            jobs={jobs}
            items={items}
            onOpen={onOpen}
            onStop={onStop}
          />
        )}
      </div>
    </div>
  );
}

/** The roster's foot: what is live, and what the listed jobs have spent. */
function RosterTotals({ jobs, partial }: { jobs: Job[]; partial: boolean }) {
  const tokens = jobs.reduce((n, j) => n + (j.usage?.totalTokens ?? 0), 0);
  const cost = jobs.reduce((n, j) => n + (j.usage?.cost ?? 0), 0);
  const label = liveJobsLabel(jobs).replace(/, /g, " · ") || "nothing live";
  return (
    <div className="text-muted-foreground flex items-center gap-2 border-t px-3 py-1.5 text-[11px]">
      <span className="min-w-0 flex-1 truncate">{label}</span>
      <span className="shrink-0 tabular-nums">
        {/* Totals over a partial list are a floor, and say so. */}
        {partial && (tokens || cost) ? "≥ " : ""}
        {[tokens ? `${fmtTokens(tokens)} tok` : "", cost ? fmtCost(cost) : ""].filter(Boolean).join(" · ")}
      </span>
    </div>
  );
}

/** The jobs roster: agents, shells and monitors running beside the conversation. */
export function JobsSurface({ threadId, command, loadAllJobs, focus }: JobsSurfaceProps) {
  // `jobs` is the roster, `items` feeds an agent's transcript pane, and
  // `jobsBefore` says the snapshot left some out.
  const state = useThread(useThreadStore(), surfaceFields, shallow);
  const tree = useMemo(() => jobTree(state.jobs), [state.jobs]);
  const [openId, setOpenId] = useState<string | null>(null);
  // Each focus is taken once, during render, so the job is open in the same
  // paint as the tab that shows it. A job not in the list yet stays asked
  // for: the roster shows until it arrives, then it opens.
  const [focusNonce, setFocusNonce] = useState<number | undefined>(undefined);
  if (focus && focus.nonce !== focusNonce) {
    setFocusNonce(focus.nonce);
    setOpenId(focus.jobId);
  }
  const open = openId ? state.jobs.find((j) => j.id === openId) : undefined;

  const stop = (j: Job) => void command("stop_job", { threadId, jobId: j.id }).catch(() => {});

  if (open) {
    return (
      <OpenJob
        threadId={threadId}
        job={open}
        jobs={state.jobs}
        items={state.items}
        command={command}
        onBack={() => setOpenId(null)}
        onOpen={(j) => setOpenId(j.id)}
        onStop={stop}
      />
    );
  }

  // Every running job is in the snapshot, so what is missing is finished work:
  // worth a quiet row at the foot of the roster, not a claim of the count —
  // most of it can be shells the roster would not show anyway.
  const missing = (state.jobsBefore ?? 0) > 0;

  if (tree.length === 0 && !(missing && loadAllJobs)) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <BotIcon className="size-5 opacity-60" />
        <p className="text-[13px]">No jobs yet.</p>
        <p className="text-[12px]">Subagents, background shells and monitors show up here.</p>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="scroll-thin min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-contain p-2">
        {SECTIONS.map(({ kind, title }) => {
          const rows = tree.filter((j) => j.kind === kind);
          if (rows.length === 0) return null;
          return (
            <section key={kind} className="space-y-1.5">
              <p className="text-muted-foreground px-1 text-[10px] font-semibold tracking-wide uppercase">{title}</p>
              {rows.map((j) => (
                <JobRow key={j.id} job={j} onOpen={(x) => setOpenId(x.id)} onStop={stop} />
              ))}
            </section>
          );
        })}
        {missing && loadAllJobs && <EarlierJobs load={loadAllJobs} />}
      </div>
      <RosterTotals jobs={state.jobs} partial={missing} />
    </div>
  );
}

const NO_JOBS: Job[] = [];
const NO_ITEMS: Item[] = [];

function surfaceFields(state: ThreadState | null) {
  return {
    jobs: state?.jobs ?? NO_JOBS,
    items: state?.items ?? NO_ITEMS,
    jobsBefore: state?.jobsBefore,
  };
}
