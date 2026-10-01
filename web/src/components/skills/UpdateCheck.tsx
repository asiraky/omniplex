import { CheckIcon, ChevronRightIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { DiffLines } from "~/components/Diff";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { diffLines } from "~/lib/lineDiff";
import { normalizeUpdateStage } from "~/lib/skillFlows";
import { errorText, type FileChange, type Skill, type UpdateFile, type UpdateStage } from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { ErrorLine, type SkillsCommand } from "./parts";
import { useStaging } from "./useStaging";

const FILE_STATUS: Record<FileChange["status"], string> = {
  added: "new",
  modified: "changed",
  removed: "removed",
};

/** One changed file, before and after, as a diff made here from the two versions. */
function FileDiff({
  command,
  scopeArgs,
  id,
  dir,
  path,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  id: string;
  dir: string;
  path: string;
}) {
  const [file, setFile] = useState<UpdateFile | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);

  useEffect(() => {
    let stale = false;
    setError("");
    commandRef
      .current<UpdateFile>("read_update_file", { ...argsRef.current, id, dir, path })
      .then((f) => {
        if (!stale) setFile(f);
      })
      .catch((e) => {
        if (!stale) setError(errorText(e));
      });
    return () => {
      stale = true;
    };
  }, [id, dir, path, retry, commandRef, argsRef]);

  const lines = useMemo(() => (file && !file.binary ? diffLines(file.old ?? "", file.new ?? "") : []), [file]);

  if (error) {
    return (
      <div className="space-y-2 pb-2">
        <ErrorLine message={error} />
        <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => setRetry((n) => n + 1)}>
          Try again
        </Button>
      </div>
    );
  }
  if (!file) {
    return (
      <p className="text-muted-foreground flex items-center gap-2 pb-2 text-[12px]">
        <Spinner className="text-primary size-3.5" /> Reading {path}
      </p>
    );
  }
  if (file.binary) {
    return <p className="text-muted-foreground pb-2 text-[12px]">A binary file, so there is nothing to show as text.</p>;
  }
  return (
    <div
      role="group"
      aria-label={`Changes in ${path}`}
      className="scroll-thin mb-2 max-h-[50dvh] overflow-auto overscroll-contain rounded-lg border"
    >
      {/* Wrapped: a phone has no room to scroll a line of prose sideways. */}
      <DiffLines lines={lines} wrap />
    </div>
  );
}

/**
 * "Check for update" for one skill, inline under the detail view's buttons:
 * fetch its source again, say what would change in this skill alone, and
 * apply it. Mounted per check: the fetch starts with the component, and the
 * fetched copy is thrown away when it goes.
 */
export function UpdateCheck({
  command,
  scopeArgs,
  skill,
  onUpdated,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  skill: Skill;
  /** The skills as the server left them after the update. */
  onUpdated: (skills: Skill[]) => void;
}) {
  const [stage, setStage] = useState<UpdateStage | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [viewing, setViewing] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);
  const [applied, setApplied] = useState(false);
  const [applyError, setApplyError] = useState("");

  const staging = useStaging(command, scopeArgs);
  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);
  // The check is about the skill it started on, whatever the detail view
  // is handed afterwards.
  const [dir] = useState(skill.dir);
  const repo = skill.source?.repo ?? "";

  useEffect(() => {
    const { wanted, hold } = staging.begin();
    setStage(null);
    setError("");
    commandRef
      .current<UpdateStage>("stage_update", { ...argsRef.current, dir })
      .then((raw) => {
        const next = normalizeUpdateStage(raw);
        if (hold(next.id)) setStage(next);
      })
      .catch((e) => {
        if (wanted()) setError(errorText(e));
      });
  }, [dir, attempt, staging, commandRef, argsRef]);

  const apply = async () => {
    if (!stage || applying) return;
    setApplying(true);
    setApplyError("");
    try {
      const res = await command<{ skills: Skill[] | null }>("apply_update", { ...scopeArgs, id: stage.id, dirs: [dir] });
      setApplied(true);
      onUpdated(res.skills ?? []);
    } catch (e) {
      setApplyError(errorText(e));
    } finally {
      setApplying(false);
    }
  };

  const box = "mt-3 space-y-2 text-[12.5px] leading-snug";

  if (error) {
    return (
      <div className={box}>
        <ErrorLine message={error} />
        <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => setAttempt((n) => n + 1)}>
          Try again
        </Button>
      </div>
    );
  }
  if (!stage) {
    return (
      <p role="status" className={cn(box, "text-muted-foreground flex items-center gap-2")}>
        <Spinner role="presentation" aria-hidden className="text-primary size-3.5" /> Checking…
      </p>
    );
  }

  const entry = stage.skills.find((s) => s.dir === dir);
  const from = stage.repo || repo;
  if (applied) {
    return (
      <p role="status" className={cn(box, "flex items-center gap-2")}>
        <CheckIcon className="text-success size-3.5" aria-hidden /> Updated
      </p>
    );
  }
  if (entry?.gone) {
    return (
      <p role="status" className={cn(box, "text-muted-foreground")}>
        {from} no longer has this skill.
      </p>
    );
  }
  if (!entry?.changed) {
    return (
      <p role="status" className={cn(box, "flex items-center gap-2")}>
        <CheckIcon className="text-success size-3.5" aria-hidden /> Up to date
      </p>
    );
  }

  const n = entry.files.length;
  return (
    <div className={box}>
      <p role="status">
        {entry.local
          ? `You edited this here. Updating replaces your edits with the copy from ${from}.`
          : `${n} ${n === 1 ? "file" : "files"} changed`}
      </p>
      <ul aria-label="Changed files">
        {entry.files.map((f) => {
          const isOpen = viewing === f.path;
          return (
            <li key={f.path}>
              <button
                type="button"
                aria-expanded={isOpen}
                onClick={() => setViewing(isOpen ? null : f.path)}
                className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full items-center gap-2 rounded-md px-1 text-left outline-none focus-visible:ring-2 md:min-h-8"
              >
                <ChevronRightIcon
                  className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", isOpen && "rotate-90")}
                  aria-hidden
                />
                <span className="min-w-0 flex-1 font-mono text-[11.5px] break-all">{f.path}</span>
                <span className="text-muted-foreground shrink-0 text-[11px]">{FILE_STATUS[f.status] ?? f.status}</span>
              </button>
              {isOpen && <FileDiff command={command} scopeArgs={scopeArgs} id={stage.id} dir={dir} path={f.path} />}
            </li>
          );
        })}
      </ul>
      <Button size="sm" className="h-11 text-[13px] md:h-8 md:text-[12px]" onClick={() => void apply()} disabled={applying}>
        {applying && <Spinner className="size-3.5" />}
        Update
      </Button>
      {applyError && <ErrorLine message={applyError} />}
    </div>
  );
}
