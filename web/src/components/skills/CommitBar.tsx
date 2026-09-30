import { CheckIcon, ChevronRightIcon, GitCommitHorizontalIcon, XIcon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import { baseName, defaultCommitMessage, normalizeGitStatus, RECORD_FILE } from "~/lib/skillFlows";
import { errorText, type GitChange, type GitStatus } from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { ErrorLine, TickRow, type SkillsContext } from "./parts";

function changeText(change: GitChange): string {
  const files = change.files > 1 ? `, ${change.files} files` : "";
  const what = change.status === "added" ? "New" : change.status === "removed" ? "Removed" : "Changed";
  return change.name === RECORD_FILE ? `${what}${files}. Records where each skill came from.` : `${what}${files}`;
}

/**
 * The personal library's uncommitted changes, when it sits in a git repo: one
 * line until it is opened, then the entries to tick, a message and Commit.
 * It commits and nothing else; pushing is left to the owner's own tools.
 *
 * The status is asked for again whenever the surface's list is replaced,
 * which is after every load and every write it folds in.
 */
export function CommitBar({ ctx }: { ctx: SkillsContext }) {
  const { command, scopeArgs, list } = ctx;
  const messageId = useId();
  const [git, setGit] = useState<GitStatus | null>(null);
  const [open, setOpen] = useState(false);
  // Entries are in unless taken out, so a change that shows up later is ticked too.
  const [unticked, setUnticked] = useState<ReadonlySet<string>>(new Set());
  // null follows the ticks; a string is what the reader typed.
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [committed, setCommitted] = useState("");

  // The newest answer wins: a status asked for before a commit must not land
  // after it and bring the committed entries back.
  const seq = useRef(0);
  const commandRef = useLatest(command);

  useEffect(() => {
    if (!list) return;
    const mine = ++seq.current;
    commandRef
      .current<{ git: GitStatus | null }>("skills_git_status", scopeArgs)
      .then((res) => {
        if (mine === seq.current) setGit(normalizeGitStatus(res.git));
      })
      // A failed check leaves the bar as it was: there is nothing to act on,
      // and the next write asks again.
      .catch(() => {});
  }, [list, scopeArgs, commandRef]);

  if (!git) return null;
  const changes = git.changes;
  if (changes.length === 0 && !committed) return null;

  const chosen = changes.filter((c) => !unticked.has(c.name));
  const message = draft ?? defaultCommitMessage(chosen);
  const repo = baseName(git.root);

  const commit = async () => {
    if (busy || chosen.length === 0 || !message.trim()) return;
    setBusy(true);
    setError("");
    try {
      const res = await command<{ commit: string; git: GitStatus | null }>("commit_skills", {
        ...scopeArgs,
        names: chosen.map((c) => c.name),
        message: message.trim(),
      });
      seq.current++;
      const next = normalizeGitStatus(res.git);
      setGit(next ?? { ...git, changes: [] });
      setCommitted(res.commit);
      setDraft(null);
      if (!next || next.changes.length === 0) setOpen(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const tick = (name: string, on: boolean) =>
    setUnticked((u) => {
      const next = new Set(u);
      if (on) next.delete(name);
      else next.add(name);
      return next;
    });

  return (
    <section aria-label="Uncommitted changes" className="shrink-0 border-b">
      {committed && (
        <div className="flex items-center gap-2 pr-1 pl-3">
          <CheckIcon className="text-success size-3.5 shrink-0" aria-hidden />
          <p role="status" className="min-w-0 flex-1 py-1 text-[12.5px] leading-snug">
            Committed <span className="font-mono text-[12px]">{committed}</span> in {repo}. Not pushed.
          </p>
          <IconButton label="Dismiss" onClick={() => setCommitted("")}>
            <XIcon />
          </IconButton>
        </div>
      )}

      {changes.length > 0 && (
        <>
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full items-center gap-2 px-3 text-left text-[12.5px] outline-none focus-visible:ring-2 focus-visible:ring-inset md:min-h-9"
          >
            <GitCommitHorizontalIcon className="text-attention size-4 shrink-0" aria-hidden />
            <span className="min-w-0 flex-1 truncate">
              {changes.length} uncommitted {changes.length === 1 ? "change" : "changes"} in {repo}
            </span>
            <ChevronRightIcon
              className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-90")}
              aria-hidden
            />
          </button>

          {open && (
            <form
              className="scroll-thin max-h-[55dvh] space-y-2 overflow-y-auto overscroll-contain px-3 pb-3"
              onSubmit={(e) => {
                e.preventDefault();
                void commit();
              }}
            >
              <ul className="divide-y rounded-lg border">
                {changes.map((c) => (
                  <li key={c.name} className="px-3">
                    <TickRow checked={!unticked.has(c.name)} onChange={(on) => tick(c.name, on)} disabled={busy}>
                      <span className="block font-mono text-[12.5px] leading-tight wrap-anywhere">{c.name}</span>
                      <span className="text-muted-foreground block text-[12px] leading-snug">{changeText(c)}</span>
                    </TickRow>
                  </li>
                ))}
              </ul>
              <Label htmlFor={messageId} className="sr-only">
                Commit message
              </Label>
              <Input
                id={messageId}
                value={message}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="Commit message"
                autoComplete="off"
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                disabled={busy}
                className="font-mono md:text-[12.5px]"
              />
              <div className="flex items-center gap-3">
                <p className="text-muted-foreground min-w-0 flex-1 text-[12px] leading-snug">
                  {git.branch ? (
                    <>
                      Commits on <span className="font-mono text-[11.5px] break-all">{git.branch}</span>. Nothing is
                      pushed.
                    </>
                  ) : (
                    "Nothing is pushed."
                  )}
                </p>
                <Button
                  type="submit"
                  size="sm"
                  className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]"
                  disabled={busy || chosen.length === 0 || !message.trim()}
                >
                  {busy && <Spinner className="size-3.5" />}
                  Commit
                </Button>
              </div>
              {error && <ErrorLine message={error} />}
            </form>
          )}
        </>
      )}
    </section>
  );
}
