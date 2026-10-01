import { ChevronRightIcon } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { defaultStagedTicks, normalizeStaged, sourceLabel } from "~/lib/skillFlows";
import {
  errorText,
  skillDescriptionError,
  skillNameError,
  type Skill,
  type SkillFileContent,
  type Staged,
  type StagedSkill,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { ErrorLine, Marker, ProblemText, type SkillsCommand } from "./parts";
import { useStaging } from "./useStaging";

const SKILL_MD = "SKILL.md";

/**
 * A fetched skill's files, read-only, SKILL.md first. Shown as source rather
 * than rendered, since this is where the reader decides whether to trust it.
 */
function StagedPreview({
  command,
  scopeArgs,
  id,
  skill,
}: {
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  id: string;
  skill: StagedSkill;
}) {
  const paths = useMemo(() => {
    const rest = skill.files.map((f) => f.path).filter((p) => p !== SKILL_MD);
    return [SKILL_MD, ...rest];
  }, [skill.files]);
  const [path, setPath] = useState(SKILL_MD);
  const [file, setFile] = useState<SkillFileContent | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  // Flipping between two files on 4G should cost one round trip each, once.
  const cache = useRef(new Map<string, SkillFileContent>());
  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);

  useEffect(() => {
    const cached = cache.current.get(path);
    setError("");
    setFile(cached ?? null);
    if (cached) return;
    let stale = false;
    commandRef
      .current<SkillFileContent>("read_staged_file", { ...argsRef.current, id, skill: skill.name, path })
      .then((f) => {
        if (stale) return;
        cache.current.set(path, f);
        setFile(f);
      })
      .catch((e) => {
        if (!stale) setError(errorText(e));
      });
    return () => {
      stale = true;
    };
  }, [path, id, skill.name, retry, commandRef, argsRef]);

  return (
    <div className="mb-2 space-y-2">
      {paths.length > 1 && (
        <ul aria-label={`Files in ${skill.name}`} className="flex flex-wrap gap-1.5">
          {paths.map((p) => (
            <li key={p} className="min-w-0">
              <button
                type="button"
                aria-current={p === path ? "true" : undefined}
                onClick={() => setPath(p)}
                className={cn(
                  "focus-visible:ring-ring flex min-h-11 max-w-full items-center rounded-md border px-2.5 font-mono text-[11.5px] break-all outline-none focus-visible:ring-2 md:min-h-7",
                  p === path ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/50",
                )}
              >
                {p}
              </button>
            </li>
          ))}
        </ul>
      )}
      {error ? (
        <div className="space-y-2">
          <ErrorLine message={error} />
          <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => setRetry((n) => n + 1)}>
            Try again
          </Button>
        </div>
      ) : !file ? (
        <p className="text-muted-foreground flex items-center gap-2 py-2 text-[12px]">
          <Spinner className="text-primary size-3.5" /> Reading {path}
        </p>
      ) : file.binary ? (
        <p className="text-muted-foreground py-2 text-[12px]">A binary file, so there is nothing to show as text.</p>
      ) : (
        <pre
          aria-label={path}
          className="bg-muted/30 scroll-thin max-h-[50dvh] overflow-auto overscroll-contain rounded-lg border px-3 py-2 font-mono text-[11.5px] leading-relaxed break-words whitespace-pre-wrap"
        >
          {file.content}
        </pre>
      )}
    </div>
  );
}

/** Name and description for a skill written from scratch. */
function NewSkillForm({
  formId,
  command,
  scopeArgs,
  busy,
  setBusy,
  onCreated,
}: {
  formId: string;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  busy: boolean;
  setBusy: (busy: boolean) => void;
  onCreated: (skill: Skill) => void;
}) {
  const id = useId();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [touched, setTouched] = useState({ name: false, description: false });
  const [error, setError] = useState("");

  const nameError = skillNameError(name);
  const descriptionError = skillDescriptionError(description);

  const submit = async () => {
    setTouched({ name: true, description: true });
    if (nameError || descriptionError || busy) return;
    setBusy(true);
    setError("");
    try {
      const skill = await command<Skill>("create_skill", { ...scopeArgs, name, description: description.trim() });
      onCreated(skill);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  // Shown as soon as there is something typed: the rules are narrow enough
  // that finding out on submit means retyping.
  const showNameError = nameError && (touched.name || name.length > 0);
  const showDescriptionError = descriptionError && touched.description;

  return (
    <form
      id={formId}
      className="scroll-thin -mx-1 min-h-0 space-y-4 overflow-y-auto px-1"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-name`}>Name</Label>
        <Input
          id={`${id}-name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => setTouched((t) => ({ ...t, name: true }))}
          placeholder="review-migrations"
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          aria-invalid={Boolean(showNameError)}
          aria-describedby={`${id}-name-hint`}
          className="font-mono"
          autoFocus
        />
        <p id={`${id}-name-hint`} className={cn("text-[11px]", showNameError ? "text-destructive" : "text-muted-foreground")}>
          {showNameError ? nameError : "Lowercase letters, digits and single hyphens, up to 64."}
        </p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-description`}>Description</Label>
        <Textarea
          id={`${id}-description`}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          onBlur={() => setTouched((t) => ({ ...t, description: true }))}
          placeholder="What it does and when an agent should use it."
          aria-invalid={Boolean(showDescriptionError)}
          aria-describedby={`${id}-description-hint`}
          className="max-h-40 min-h-20"
        />
        <p
          id={`${id}-description-hint`}
          className={cn("text-[11px]", showDescriptionError ? "text-destructive" : "text-muted-foreground")}
        >
          {showDescriptionError
            ? descriptionError
            : `Agents read this to decide when to use the skill. ${description.trim().length}/1024`}
        </p>
      </div>
      {error && <ErrorLine message={error} />}
    </form>
  );
}

/**
 * Add a skill: paste a source, fetch it, tick what to keep and install it;
 * or switch to a blank form and write a new one. Keyed per opening by the
 * caller, so each one starts at the paste box.
 */
export function AddSheet({
  open,
  onOpenChange,
  command,
  scopeArgs,
  onInstalled,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  /** The skills as the server installed them. */
  onInstalled: (skills: Skill[]) => void;
  onCreated: (skill: Skill) => void;
}) {
  const formId = useId();
  const sourceId = useId();
  const [mode, setMode] = useState<"install" | "new">("install");
  const [source, setSource] = useState("");
  // What is being fetched, by name; "" when nothing is.
  const [fetching, setFetching] = useState("");
  const [staged, setStaged] = useState<Staged | null>(null);
  const [error, setError] = useState("");
  const [ticked, setTicked] = useState<ReadonlySet<string>>(new Set());
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);

  const staging = useStaging(command, scopeArgs);

  const fetchSource = async () => {
    const text = source.trim();
    if (!text || fetching) return;
    const { wanted, hold } = staging.begin();
    setFetching(sourceLabel(text));
    setError("");
    try {
      const next = normalizeStaged(await command<Staged>("stage_skills", { ...scopeArgs, source: text }));
      if (!hold(next.id)) return;
      setFetching("");
      if (next.skills.length === 0) {
        staging.release();
        setError(`No skills were found in ${next.repo || sourceLabel(text)}.`);
        return;
      }
      setStaged(next);
      setTicked(new Set(defaultStagedTicks(next.skills)));
      setExpanded(new Set());
    } catch (e) {
      if (!wanted()) return;
      setFetching("");
      setError(errorText(e));
    }
  };

  const cancelFetch = () => {
    staging.release();
    setFetching("");
  };

  const close = () => {
    if (busy) return;
    staging.release();
    onOpenChange(false);
  };

  const writeNew = () => {
    staging.release();
    setFetching("");
    setStaged(null);
    setError("");
    setMode("new");
  };

  const chosen = staged ? staged.skills.filter((s) => ticked.has(s.name)).map((s) => s.name) : [];

  const install = async () => {
    if (!staged || chosen.length === 0 || busy) return;
    setBusy(true);
    setError("");
    try {
      const res = await command<{ skills: Skill[] | null }>("install_staged", {
        ...scopeArgs,
        id: staged.id,
        skills: chosen,
      });
      // Installing is what empties the fetched copy on the server.
      staging.forget();
      onInstalled(res.skills ?? []);
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = (set: ReadonlySet<string>, item: string, on: boolean): ReadonlySet<string> => {
    const next = new Set(set);
    if (on) next.add(item);
    else next.delete(item);
    return next;
  };

  const allTicked = staged !== null && chosen.length === staged.skills.length;

  let body;
  let footer;
  if (mode === "new") {
    body = (
      <NewSkillForm
        formId={formId}
        command={command}
        scopeArgs={scopeArgs}
        busy={busy}
        setBusy={setBusy}
        onCreated={(skill) => {
          onCreated(skill);
          onOpenChange(false);
        }}
      />
    );
    footer = (
      <>
        <Button variant="outline" onClick={() => setMode("install")} disabled={busy}>
          Back
        </Button>
        <Button type="submit" form={formId} disabled={busy}>
          {busy && <Spinner className="size-3.5" />}
          Create
        </Button>
      </>
    );
  } else if (fetching) {
    body = (
      <div className="min-h-0 space-y-2 overflow-y-auto py-6 text-center" role="status">
        <p className="flex items-center justify-center gap-2 text-[13px]">
          {/* The line is the status; a second one inside it would be read twice. */}
          <Spinner role="presentation" aria-hidden className="text-primary size-4 shrink-0" />
          <span className="min-w-0 break-words">Fetching {fetching}</span>
        </p>
        <p className="text-muted-foreground text-[12px] leading-snug">This can take a minute.</p>
      </div>
    );
    footer = (
      <Button variant="outline" onClick={cancelFetch}>
        Cancel
      </Button>
    );
  } else if (!staged) {
    body = (
      <form
        id={formId}
        className="scroll-thin -mx-1 min-h-0 space-y-2 overflow-y-auto px-1"
        onSubmit={(e) => {
          e.preventDefault();
          void fetchSource();
        }}
      >
        <Label htmlFor={sourceId} className="leading-snug font-normal">
          Paste owner/repo, a GitHub URL, a folder path, or an npx skills add command
        </Label>
        <Textarea
          id={sourceId}
          value={source}
          onChange={(e) => setSource(e.target.value)}
          onKeyDown={(e) => {
            // One line of input that happens to wrap: Enter sends it.
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void fetchSource();
            }
          }}
          placeholder="owner/repo"
          rows={2}
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          className="min-h-16 font-mono break-all"
          autoFocus
        />
        {error && <ErrorLine message={error} />}
        <button
          type="button"
          onClick={writeNew}
          className="text-primary focus-visible:ring-ring -mx-1 flex min-h-11 items-center rounded-md px-1 text-[13px] underline-offset-2 outline-none hover:underline focus-visible:ring-2 md:min-h-8"
        >
          or write a new skill
        </button>
      </form>
    );
    footer = (
      <>
        <Button variant="outline" onClick={close}>
          Cancel
        </Button>
        <Button type="submit" form={formId} disabled={!source.trim()}>
          Fetch
        </Button>
      </>
    );
  } else {
    body = (
      <div className="scroll-thin -mx-1 min-h-0 space-y-3 overflow-y-auto px-1">
        <div className="flex items-center gap-2">
          <p className="min-w-0 flex-1 text-[12.5px] leading-snug break-words">
            {staged.skills.length} {staged.skills.length === 1 ? "skill" : "skills"} in{" "}
            <span className="font-mono text-[12px]">{staged.repo}</span>
          </p>
          {staged.skills.length > 1 && (
            <Button
              variant="ghost"
              size="sm"
              className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
              disabled={busy}
              onClick={() => setTicked(new Set(allTicked ? [] : staged.skills.map((s) => s.name)))}
            >
              {allTicked ? "Clear all" : "Select all"}
            </Button>
          )}
        </div>
        <ul aria-label="Skills found" className="divide-y rounded-lg border">
          {staged.skills.map((s) => {
            const isOpen = expanded.has(s.name);
            return (
              <li key={s.name} className="px-2">
                <div className="flex items-start">
                  {/* The box gets a thumb-sized target of its own: the name beside it opens the preview. */}
                  <label className="flex size-11 shrink-0 cursor-pointer items-center justify-center">
                    <Checkbox
                      aria-label={`Install ${s.name}`}
                      checked={ticked.has(s.name)}
                      disabled={busy}
                      onCheckedChange={(v) => setTicked((t) => toggle(t, s.name, v === true))}
                      className="size-5 md:size-4"
                    />
                  </label>
                  <button
                    type="button"
                    aria-expanded={isOpen}
                    onClick={() => setExpanded((e) => toggle(e, s.name, !isOpen))}
                    className="focus-visible:ring-ring flex min-h-11 min-w-0 flex-1 flex-col items-start gap-0.5 rounded-md py-2 pr-1 text-left outline-none focus-visible:ring-2"
                  >
                    <span className="flex w-full min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
                      {/* The spaces are for the button's spoken name; flex drops them on screen. */}
                      <span className="min-w-0 font-mono text-[13px] wrap-anywhere">{s.name}</span>{" "}
                      {s.installed && <Marker tone="attention">replaces yours</Marker>}
                      <ChevronRightIcon
                        aria-hidden
                        className={cn("text-muted-foreground ml-auto size-3.5 shrink-0 transition-transform", isOpen && "rotate-90")}
                      />
                    </span>{" "}
                    {s.description && (
                      <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">{s.description}</span>
                    )}
                    <ProblemText problem={s.problem} />
                  </button>
                </div>
                {isOpen && <StagedPreview command={command} scopeArgs={scopeArgs} id={staged.id} skill={s} />}
              </li>
            );
          })}
        </ul>
        {error && <ErrorLine message={error} />}
      </div>
    );
    footer = (
      <>
        <Button variant="outline" onClick={close} disabled={busy}>
          Cancel
        </Button>
        <Button onClick={() => void install()} disabled={chosen.length === 0 || busy}>
          {busy && <Spinner className="size-3.5" />}
          {chosen.length > 0 ? `Install ${chosen.length}` : "Install"}
        </Button>
      </>
    );
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent
        fullscreenOnMobile
        aria-describedby={undefined}
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-xl md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">{mode === "new" ? "New skill" : "Add skills"}</DialogTitle>
        </DialogHeader>
        {body}
        <DialogFooter>{footer}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
