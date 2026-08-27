import { ArrowLeftIcon, BotIcon, SquareIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { Markdown } from "~/components/Markdown";
import { fmtTokens } from "~/lib/format";
import { childJobs, isLive, jobLabel, jobTree, liveJobsLabel } from "~/lib/jobs";
import { cn } from "~/lib/utils";
import type { Item, Job, JobKind, SessionState } from "~/protocol";
import { formatDuration } from "~/rows";

export interface JobsSurfaceProps {
  sessionId: string;
  /** The session: `jobs` is the roster, `items` feeds an agent's transcript pane. */
  state: SessionState;
  /** A ws command: `stop_job` and `session_job_output`. */
  command: (command: string, args: unknown) => Promise<any>;
}

// A live elapsed timer written straight to the DOM: re-rendering the roster
// once a second to move a clock would fight the fixed-row design it sits in.
// Frozen at `finishedAt - since` once the job is done.
function Elapsed({ since, finishedAt }: { since?: number; finishedAt?: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!since) return;
    const el = ref.current;
    if (!el) return;
    const tick = () => {
      el.textContent = formatDuration((finishedAt ?? Date.now()) - since);
    };
    tick();
    if (finishedAt) return;
    const t = window.setInterval(tick, 1000);
    return () => window.clearInterval(t);
  }, [since, finishedAt]);
  if (!since) return null;
  return <span ref={ref} className="tabular-nums" />;
}

function StatusMark({ job }: { job: Job }) {
  const s = job.status;
  return (
    <span
      className={cn(
        "mt-1.5 size-2 shrink-0 rounded-full",
        s === "running" && "bg-primary animate-pulse",
        s === "paused" && "bg-warning ring-warning/40 ring-2",
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

function usageParts(job: Job): string[] {
  const out: string[] = [];
  if (job.usage?.totalTokens) out.push(`${fmtTokens(job.usage.totalTokens)} tok`);
  if (job.usage?.cost) out.push(fmtCost(job.usage.cost));
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
  const meta = [
    job.kind === "agent" ? job.taskType : undefined,
    <Elapsed key="t" since={job.startedAt} finishedAt={job.finishedAt} />,
    ...usageParts(job),
  ].filter(Boolean);
  const second = live ? job.activity || "working…" : job.error || job.status;

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
            {meta.map((part, i) => (
              <span key={i}>
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
  sessionId,
  job,
  command,
}: {
  sessionId: string;
  job: Job;
  command: JobsSurfaceProps["command"];
}) {
  const preRef = useRef<HTMLPreElement>(null);
  const [text, setText] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!job.outputFile) return;
    let offset = 0;
    let stopped = false;
    let timer = 0;
    const poll = async () => {
      try {
        const res = (await command("session_job_output", { sessionId, jobId: job.id, offset })) as {
          text: string;
          offset: number;
          done: boolean;
        };
        if (stopped) return;
        offset = res.offset;
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
  }, [sessionId, job.id, job.outputFile, command]);

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
  job,
  jobs,
  items,
  onOpen,
}: {
  job: Job;
  jobs: Job[];
  items: Item[];
  onOpen: (j: Job) => void;
}) {
  const mine = useMemo(
    () => (job.toolCallId ? items.filter((it) => it.parentId === job.toolCallId) : []),
    [items, job.toolCallId],
  );
  const children = useMemo(() => childJobs(jobs, job.id), [jobs, job.id]);

  return (
    <div className="scroll-thin h-full space-y-1.5 overflow-y-auto overscroll-contain p-2">
      {mine.length === 0 && children.length === 0 && (
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
            <JobRow key={c.id} job={{ ...c, depth: 0 }} onOpen={onOpen} onStop={() => {}} />
          ))}
        </>
      )}
    </div>
  );
}

/** The jobs roster: agents, shells and monitors running beside the conversation. */
export function JobsSurface({ sessionId, state, command }: JobsSurfaceProps) {
  const tree = useMemo(() => jobTree(state.jobs), [state.jobs]);
  const [openId, setOpenId] = useState<string | null>(null);
  const open = openId ? state.jobs.find((j) => j.id === openId) : undefined;

  const stop = (j: Job) => void command("stop_job", { sessionId, jobId: j.id }).catch(() => {});

  if (open) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex min-h-11 items-center gap-1 border-b pr-2">
          <button
            type="button"
            aria-label="Back to jobs"
            onClick={() => setOpenId(null)}
            className="hover:bg-accent/50 flex min-h-11 min-w-11 shrink-0 items-center justify-center outline-none"
          >
            <ArrowLeftIcon className="size-4" />
          </button>
          <StatusMark job={open} />
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium">{jobLabel(open)}</span>
          <span className="text-muted-foreground shrink-0 text-[10px]">
            <Elapsed since={open.startedAt} finishedAt={open.finishedAt} />
          </span>
          {isLive(open) && (
            <button
              type="button"
              aria-label={`Stop ${jobLabel(open)}`}
              onClick={() => stop(open)}
              className="text-muted-foreground hover:text-destructive flex min-h-11 min-w-11 items-center justify-center outline-none"
            >
              <SquareIcon className="size-3.5 fill-current" />
            </button>
          )}
        </div>
        <div className="min-h-0 flex-1">
          {open.kind === "shell" ? (
            <ShellPane key={open.id} sessionId={sessionId} job={open} command={command} />
          ) : (
            <AgentPane job={open} jobs={state.jobs} items={state.items} onOpen={(j) => setOpenId(j.id)} />
          )}
        </div>
      </div>
    );
  }

  if (tree.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <BotIcon className="size-5 opacity-60" />
        <p className="text-[13px]">No jobs yet.</p>
        <p className="text-[12px]">Subagents, background shells and monitors show up here.</p>
      </div>
    );
  }

  const tokens = state.jobs.reduce((n, j) => n + (j.usage?.totalTokens ?? 0), 0);
  const cost = state.jobs.reduce((n, j) => n + (j.usage?.cost ?? 0), 0);
  const label = liveJobsLabel(state.jobs).replace(/, /g, " · ") || "nothing live";

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
      </div>
      <div className="text-muted-foreground flex items-center gap-2 border-t px-3 py-1.5 text-[11px]">
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <span className="shrink-0 tabular-nums">
          {[tokens ? `${fmtTokens(tokens)} tok` : "", cost ? fmtCost(cost) : ""].filter(Boolean).join(" · ")}
        </span>
      </div>
    </div>
  );
}
