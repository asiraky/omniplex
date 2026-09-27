import { ChevronDownIcon } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { Badge } from "~/components/ui/badge";
import { Input } from "~/components/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { cn } from "~/lib/utils";
import type { Issue, UserConfig, Workspace } from "~/protocol";

export interface WorkspaceChoice {
  /** What the user typed: the branch to create. Empty means "let omniplex name it". */
  branch: string;
  /** Set only when attaching to a checkout that already exists. */
  attachPath: string;
}

/**
 * Turn the user's format function into a callable. It is their own code from
 * their own settings file, evaluated in their own browser, so `new Function` is
 * no wider a door than the settings box already is — but a typo in it must not
 * take the dialog down, hence the two layers of try/catch.
 */
export function makeFormatter(source: string): {
  format: (issue: Issue) => string;
  error: string | null;
} {
  const fallback = { format: (i: Issue) => `issue/${i.number}`, error: null as string | null };
  if (!source.trim()) return fallback;
  let fn: (issue: Issue) => unknown;
  try {
    fn = new Function("issue", `"use strict"; return (${source})(issue);`) as (
      issue: Issue,
    ) => unknown;
  } catch (e) {
    return { ...fallback, error: e instanceof Error ? e.message : String(e) };
  }
  return {
    error: null,
    format: (issue: Issue) => {
      try {
        const out = fn(issue);
        return typeof out === "string" ? out : String(out ?? "");
      } catch {
        return "";
      }
    },
  };
}

type Row =
  | { kind: "create"; branch: string }
  | { kind: "issue"; branch: string; issue: Issue }
  | { kind: "existing"; workspace: Workspace };

const rowKey = (r: Row) =>
  r.kind === "existing"
    ? `w:${r.workspace.path}`
    : r.kind === "issue"
      ? `i:${r.issue.number}`
      : "create";
const label = (w: Workspace) =>
  w.branch || (w.head ? `detached @ ${w.head}` : w.path.split("/").pop() || w.path);

/**
 * One combobox, wearing whichever half of the question was asked of it. In
 * `create` mode typing names a worktree to make and the open issues are
 * suggestions; in `attach` mode typing filters the checkouts that already
 * exist. Which of those the user wants is decided a level up, by the workspace
 * choice they picked, so this control never has to guess between them.
 */
export function WorkspacePicker({
  id,
  mode,
  value,
  onChange,
  workspaces,
  issues,
  issuesError,
  userConfig,
  loading,
  placeholder,
}: {
  id?: string;
  mode: "create" | "attach";
  value: WorkspaceChoice;
  onChange: (v: WorkspaceChoice) => void;
  workspaces: Workspace[];
  issues: Issue[];
  issuesError: string;
  userConfig: UserConfig | null;
  loading: boolean;
  placeholder: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  const listId = useId();

  const attached = value.attachPath
    ? workspaces.find((w) => w.path === value.attachPath)
    : undefined;
  const text = attached ? label(attached) : value.branch;

  const formatter = useMemo(
    () => makeFormatter(userConfig?.branchFormat ?? ""),
    [userConfig?.branchFormat],
  );

  const rows = useMemo<Row[]>(() => {
    const typed = value.attachPath ? "" : value.branch.trim();
    const needle = typed.toLowerCase();
    const out: Row[] = [];
    if (mode === "attach") {
      for (const w of workspaces) {
        if (
          needle &&
          !label(w).toLowerCase().includes(needle) &&
          !w.path.toLowerCase().includes(needle)
        )
          continue;
        out.push({ kind: "existing", workspace: w });
      }
      return out;
    }
    if (typed) out.push({ kind: "create", branch: typed });
    if (userConfig?.suggestIssues !== false) {
      for (const issue of issues) {
        const branch = formatter.format(issue);
        if (!branch || branch === typed) continue;
        if (
          needle &&
          !branch.toLowerCase().includes(needle) &&
          !issue.title.toLowerCase().includes(needle)
        )
          continue;
        out.push({ kind: "issue", branch, issue });
      }
    }
    return out;
  }, [mode, value.branch, value.attachPath, issues, workspaces, formatter, userConfig?.suggestIssues]);

  // Every row is selectable. A checkout somebody else is in is a warning the
  // caller renders, not a door omniplex locks: nothing about Git stops two threads
  // sharing one, only their own edits do.
  useEffect(() => {
    setActive(0);
  }, [rows.length]);
  // The list lives in a portal, so keyboard moves scroll it by element id.
  useEffect(() => {
    if (open) document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: "nearest" });
  }, [open, active, listId]);

  const choose = (r: Row) => {
    if (r.kind === "existing") onChange({ branch: "", attachPath: r.workspace.path });
    else onChange({ branch: r.branch, attachPath: "" });
    setOpen(false);
  };

  const key = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      // Consume it while the list is open, so it closes the list and not the
      // whole dialog around it.
      if (open) e.stopPropagation();
      setOpen(false);
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      if (rows.length === 0) return;
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive((active + step + rows.length) % rows.length);
      return;
    }
    // Enter picks the highlighted row only while the list is open; otherwise it
    // falls through to the dialog, where it starts the thread.
    if (e.key === "Enter" && open && rows[active]) {
      e.preventDefault();
      choose(rows[active]);
    }
  };

  const group = (title: string) => (
    <div
      key={`h:${title}`}
      role="presentation"
      className="text-muted-foreground px-3 pt-2 pb-1 text-[10px] tracking-wide uppercase"
    >
      {title}
    </div>
  );
  const body: React.ReactNode[] = [];
  let last = "";
  rows.forEach((r, i) => {
    const heading =
      r.kind === "existing"
        ? "Existing copies"
        : r.kind === "issue"
          ? "From open issues"
          : "New branch";
    if (heading !== last) {
      body.push(group(heading));
      last = heading;
    }
    const busy = r.kind === "existing" && r.workspace.busy;
    body.push(
      <button
        key={rowKey(r)}
        id={`${listId}-${i}`}
        role="option"
        aria-selected={i === active}
        type="button"
        onMouseEnter={() => setActive(i)}
        onClick={() => choose(r)}
        className={cn(
          "flex min-h-11 w-full flex-col justify-center gap-0.5 px-3 py-1.5 text-left text-[13px] md:min-h-0",
          i === active && "bg-accent text-accent-foreground",
        )}
      >
        <span className="flex w-full items-baseline gap-2">
          <span className="min-w-0 truncate">
            {r.kind === "existing" ? label(r.workspace) : r.branch}
          </span>
          {r.kind === "existing" && r.workspace.isRoot && (
            <span className="text-muted-foreground shrink-0">project root</span>
          )}
          {busy && <span className="text-attention-foreground ml-auto shrink-0">in use</span>}
          {r.kind === "existing" && r.workspace.locked && !busy && (
            <span className="text-muted-foreground ml-auto shrink-0">locked</span>
          )}
        </span>
        {r.kind === "issue" && (
          <span className="flex w-full min-w-0 items-center gap-1.5">
            <span className="text-muted-foreground min-w-0 truncate text-[11px]">
              #{r.issue.number} {r.issue.title}
            </span>
            {r.issue.labels?.slice(0, 3).map((l) => (
              <Badge
                key={l.name}
                variant="outline"
                className="shrink-0 px-1.5 py-0 text-[10px] font-normal"
              >
                {l.name}
              </Badge>
            ))}
          </span>
        )}
      </button>,
    );
  });

  return (
    <div ref={box}>
      {/* The list is portaled out of the dialog, which clips and scrolls its
          own children — a dropdown has to float over it, not fight it. */}
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverAnchor asChild>
          {/* Styled like a SelectTrigger: the chevron lives inside the field,
              and clicking anywhere in it opens the list. */}
          <div className="relative">
            <Input
              id={id}
              value={text}
              onKeyDown={key}
              onFocus={() => setOpen(true)}
              onClick={() => setOpen(true)}
              onChange={(e) => onChange({ branch: e.target.value, attachPath: "" })}
              placeholder={placeholder}
              role="combobox"
              aria-expanded={open}
              aria-controls={listId}
              aria-autocomplete="list"
              aria-activedescendant={open && rows[active] ? `${listId}-${active}` : undefined}
              // 16px on touch screens: iOS Safari auto-zooms any focused field
              // smaller than that, which distorts the page and leaves the
              // portaled list misaligned with where taps actually land.
              className="w-full pr-8 text-[16px] md:text-[13px]"
            />
            <ChevronDownIcon
              aria-hidden
              className={cn(
                "pointer-events-none absolute top-1/2 right-2.5 size-4 -translate-y-1/2 opacity-50 transition-transform",
                open && "rotate-180",
              )}
            />
          </div>
        </PopoverAnchor>

        <PopoverContent
          id={listId}
          role="listbox"
          align="start"
          collisionPadding={8}
          // Typing is the search box, so focus must stay in the input; the
          // anchor is likewise exempt from outside-interaction dismissal or
          // clicking the input would close the list it just opened.
          onOpenAutoFocus={(e) => e.preventDefault()}
          onInteractOutside={(e) => {
            if (box.current?.contains(e.target as Node)) e.preventDefault();
          }}
          // The dialog's scroll lock (react-remove-scroll) preventDefaults any
          // wheel or touchmove that bubbles to document from outside the dialog
          // subtree — which this portaled list is. Its listeners are
          // bubble-phase, so stopping propagation here lets the list scroll
          // natively; overscroll containment keeps the gesture from chaining
          // to the page behind.
          onWheel={(e) => e.stopPropagation()}
          onTouchMove={(e) => e.stopPropagation()}
          className="scroll-thin max-h-[min(16rem,var(--radix-popover-content-available-height))] w-[var(--radix-popover-trigger-width)] touch-pan-y overscroll-contain overflow-y-auto p-0 py-1"
        >
          {loading && (
            <p className="text-muted-foreground px-3 py-2 text-[12px]">Loading copies…</p>
          )}
          {!loading && rows.length === 0 && (
            <p className="text-muted-foreground px-3 py-2 text-[12px]">
              {mode === "attach"
                ? "No copies of this folder yet."
                : "Type a branch name."}
            </p>
          )}
          {body}
          {formatter.error && (
            <p className="text-attention-foreground px-3 py-2 text-[11px]">
              Branch format function: {formatter.error}
            </p>
          )}
          {issuesError && !formatter.error && (
            <p className="text-muted-foreground px-3 py-2 text-[11px]">
              No issue suggestions: {issuesError}
            </p>
          )}
        </PopoverContent>
      </Popover>

      <p className="text-muted-foreground mt-1.5 text-[11px]">
        {mode === "attach"
          ? attached
            ? `Attaching to ${attached.path}`
            : "Pick a copy from the list."
          : value.branch.trim()
            ? "The copy gets this branch."
            : "Leave it blank and Omniplex makes one up."}
      </p>
    </div>
  );
}
