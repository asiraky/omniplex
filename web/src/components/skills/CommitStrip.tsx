import { GitCommitHorizontalIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { defaultCommitMessage, normalizeGitStatus } from "~/lib/skillFlows";
import { errorText, type GitStatus } from "~/lib/skills";
import { useLatest } from "~/useLatest";

import { ErrorLine, type SkillsCommand } from "./parts";

/**
 * One line when the personal library is a git repo with uncommitted skill
 * changes, and a Commit that takes all of them with a written-for-you
 * message. It commits and nothing else; pushing is left to the owner.
 *
 * `version` changes after every load and every write, and each change asks
 * git again.
 */
export function CommitStrip({
  command,
  scopeArgs,
  version,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  version: unknown;
}) {
  const [git, setGit] = useState<GitStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The newest answer wins: a status asked for before a commit must not land
  // after it and bring the committed changes back.
  const seq = useRef(0);
  const commandRef = useLatest(command);

  useEffect(() => {
    if (!version) return;
    const mine = ++seq.current;
    commandRef
      .current<{ git: GitStatus | null }>("skills_git_status", scopeArgs)
      .then((res) => {
        if (mine === seq.current) setGit(normalizeGitStatus(res.git));
      })
      // A failed check leaves the strip as it was; the next write asks again.
      .catch(() => {});
  }, [version, scopeArgs, commandRef]);

  if (!git || git.changes.length === 0) return null;
  const changes = git.changes;

  const commit = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const res = await command<{ git: GitStatus | null }>("commit_skills", {
        ...scopeArgs,
        names: changes.map((c) => c.name),
        message: defaultCommitMessage(changes),
      });
      seq.current++;
      setGit(normalizeGitStatus(res.git));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const n = changes.length;
  return (
    <section aria-label="Changes not committed" className="shrink-0 border-b px-3 py-1">
      <div className="flex min-h-11 items-center gap-2 md:min-h-9">
        <GitCommitHorizontalIcon className="text-attention size-4 shrink-0" aria-hidden />
        <p className="min-w-0 flex-1 truncate text-[12.5px]">
          {n} skill {n === 1 ? "change" : "changes"} not committed
        </p>
        <Button
          size="sm"
          variant="outline"
          className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]"
          onClick={() => void commit()}
          disabled={busy}
        >
          {busy && <Spinner className="size-3.5" />}
          Commit
        </Button>
      </div>
      {error && <ErrorLine message={error} className="pb-1" />}
    </section>
  );
}
