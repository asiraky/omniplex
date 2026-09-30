import { ChevronRightIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { DiffLines } from "~/components/Diff";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Spinner } from "~/components/ui/spinner";
import { diffLines } from "~/lib/lineDiff";
import { normalizeUpdateStage, pendingUpdates } from "~/lib/skillFlows";
import {
  errorText,
  type FileChange,
  type Skill,
  type UpdateFile,
  type UpdateSkill,
  type UpdateStage,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { ErrorLine, Marker, type SkillsCommand } from "./parts";
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

function UpdateRow({
  command,
  scopeArgs,
  id,
  skill,
  repo,
  applied,
  busy,
  disabled,
  onUpdate,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  id: string;
  skill: UpdateSkill;
  repo: string;
  applied: boolean;
  /** This skill is the one being written. */
  busy: boolean;
  disabled: boolean;
  onUpdate: () => void;
}) {
  const [viewing, setViewing] = useState<string | null>(null);
  const pending = skill.changed && !skill.gone && !applied;
  return (
    <li className="px-3 py-1">
      <div className="flex min-h-11 items-center gap-2">
        <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1.5 gap-y-1">
          <span className="min-w-0 font-mono text-[13px] break-all">{skill.name}</span>
          {applied ? (
            <Marker>updated</Marker>
          ) : skill.gone ? (
            <Marker tone="attention">gone from the source</Marker>
          ) : skill.changed ? (
            <Marker tone="attention">changed</Marker>
          ) : (
            <Marker>up to date</Marker>
          )}
        </span>
        {pending && (
          <Button
            variant="outline"
            size="sm"
            className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
            aria-label={`Update ${skill.name}`}
            disabled={disabled}
            onClick={onUpdate}
          >
            {busy && <Spinner className="size-3.5" />}
            Update
          </Button>
        )}
      </div>
      {skill.gone && !applied && (
        <p className="text-muted-foreground pb-2 text-[12px] leading-snug">
          {repo} no longer has it. Your copy stays as it is.
        </p>
      )}
      {pending && (
        <ul aria-label={`Changed files in ${skill.name}`}>
          {skill.files.map((f) => {
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
                {isOpen && <FileDiff command={command} scopeArgs={scopeArgs} id={id} dir={skill.dir} path={f.path} />}
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}

/**
 * Fetch a skill's source again and show what it would change in every skill
 * installed from it. Mounted per opening: the check starts with the dialog.
 */
export function UpdateDialog({
  open,
  onOpenChange,
  command,
  scopeArgs,
  dir,
  repo,
  onUpdated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  /** Any skill installed from the source; the server finds the rest. */
  dir: string;
  repo: string;
  /** The skills as the server left them after an update. */
  onUpdated: (skills: Skill[]) => void;
}) {
  const [stage, setStage] = useState<UpdateStage | null>(null);
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [applied, setApplied] = useState<ReadonlySet<string>>(new Set());
  // The dirs being written; null when nothing is.
  const [applying, setApplying] = useState<string[] | null>(null);
  const [applyError, setApplyError] = useState("");

  const staging = useStaging(command, scopeArgs);
  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);
  // The dialog is about the skill it was opened on. It stays mounted after it
  // closes, and a list that shifts underneath must not start another fetch.
  const [target] = useState(dir);

  useEffect(() => {
    const { wanted, hold } = staging.begin();
    setChecking(true);
    setError("");
    commandRef
      .current<UpdateStage>("stage_update", { ...argsRef.current, dir: target })
      .then((raw) => {
        const next = normalizeUpdateStage(raw);
        if (!hold(next.id)) return;
        setStage(next);
        setApplied(new Set());
        setChecking(false);
      })
      .catch((e) => {
        if (!wanted()) return;
        setError(errorText(e));
        setChecking(false);
      });
  }, [target, attempt, staging, commandRef, argsRef]);

  const close = () => {
    if (applying) return;
    staging.release();
    onOpenChange(false);
  };

  const pending = stage ? pendingUpdates(stage, applied) : [];

  const apply = async (dirs: string[]) => {
    if (!stage || applying || dirs.length === 0) return;
    setApplying(dirs);
    setApplyError("");
    try {
      const res = await command<{ skills: Skill[] | null }>("apply_update", { ...scopeArgs, id: stage.id, dirs });
      setApplied((a) => new Set([...a, ...dirs]));
      onUpdated(res.skills ?? []);
    } catch (e) {
      setApplyError(errorText(e));
    } finally {
      setApplying(null);
    }
  };

  const from = stage?.repo || repo;
  const total = stage?.skills.length ?? 0;
  const summary = !stage
    ? ""
    : applied.size > 0 && pending.length === 0
      ? `Updated ${applied.size} ${applied.size === 1 ? "skill" : "skills"}.`
      : pending.length === 0
        ? `Nothing to update. ${total === 1 ? "The skill" : `All ${total} skills`} from this source match it.`
        : `${pending.length} of ${total} ${total === 1 ? "skill" : "skills"} can be updated.`;

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent
        fullscreenOnMobile
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-2xl md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">
            Updates from <span className="font-mono text-[14px] break-all">{from}</span>
          </DialogTitle>
          <DialogDescription className="text-[12px]">
            Updating replaces a skill's files with the source's. Whether it is manual only is kept.
          </DialogDescription>
        </DialogHeader>

        {checking ? (
          <div className="min-h-0 space-y-2 overflow-y-auto py-6 text-center" role="status">
            <p className="flex items-center justify-center gap-2 text-[13px]">
              {/* The line is the status; a second one inside it would be read twice. */}
              <Spinner role="presentation" aria-hidden className="text-primary size-4 shrink-0" />
              <span className="min-w-0 break-words">Fetching {from}</span>
            </p>
            <p className="text-muted-foreground text-[12px] leading-snug">
              This can take a minute. It carries on if your connection drops.
            </p>
          </div>
        ) : error || !stage ? (
          <div className="min-h-0 space-y-2 overflow-y-auto">
            <ErrorLine message={error || "The check returned nothing."} />
            <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => setAttempt((n) => n + 1)}>
              Try again
            </Button>
          </div>
        ) : (
          <div className="scroll-thin -mx-1 min-h-0 space-y-3 overflow-y-auto px-1">
            <p role="status" className="text-[12.5px] leading-snug">
              {summary}
            </p>
            {total > 0 && (
              <ul className="divide-y rounded-lg border">
                {stage.skills.map((s) => (
                  <UpdateRow
                    key={s.dir}
                    command={command}
                    scopeArgs={scopeArgs}
                    id={stage.id}
                    skill={s}
                    repo={from}
                    applied={applied.has(s.dir)}
                    busy={applying?.length === 1 && applying[0] === s.dir}
                    disabled={applying !== null}
                    onUpdate={() => void apply([s.dir])}
                  />
                ))}
              </ul>
            )}
            {applyError && <ErrorLine message={applyError} />}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={close} disabled={applying !== null}>
            {checking ? "Cancel" : "Close"}
          </Button>
          {pending.length > 1 && (
            <Button onClick={() => void apply(pending.map((s) => s.dir))} disabled={applying !== null}>
              {applying !== null && applying.length > 1 && <Spinner className="size-3.5" />}
              Update all {pending.length}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
