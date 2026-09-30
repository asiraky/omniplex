import { ChevronRightIcon } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import {
  defaultStagedTicks,
  installLinks,
  installPath,
  normalizeStaged,
  sourceLabel,
  stagedClashes,
} from "~/lib/skillFlows";
import {
  errorText,
  harnessLabel,
  joinWords,
  type InstallScope,
  type Setup,
  type Skill,
  type SkillFileContent,
  type SkillHarness,
  type Staged,
  type StagedSkill,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { ErrorLine, Marker, ProblemText, SectionHeading, TickRow, type SkillsCommand } from "./parts";
import { useStaging } from "./useStaging";

const SCOPES: { id: InstallScope; label: string }[] = [
  { id: "user", label: "Personal" },
  { id: "project", label: "Project" },
];

const SKILL_MD = "SKILL.md";

/**
 * A staged skill's files, read-only, straight from the holding folder: what
 * is about to be installed, as text, SKILL.md first. Shown as source rather
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
    // Full width on a phone: indented under the name, a line of SKILL.md
    // would be a few words long.
    <div className="mb-2 space-y-2 md:ml-7">
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

/**
 * Install from a pasted source: fetch it into a holding folder on the server,
 * show what came back, and place only what is ticked. Keyed per opening by
 * the caller, so each one starts at the paste box.
 */
export function InstallDialog({
  open,
  onOpenChange,
  command,
  scopeArgs,
  setup,
  projectRoot,
  projectAvailable,
  onInstalled,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  /** Names the real paths and how each harness reaches them. Absent from an older server. */
  setup?: Setup;
  projectRoot?: string;
  projectAvailable: boolean;
  /** The skills as the server placed them, and which library they went into. */
  onInstalled: (skills: Skill[], scope: InstallScope) => void;
}) {
  const sourceId = useId();
  const [source, setSource] = useState("");
  // What is being fetched, by name; "" when nothing is.
  const [fetching, setFetching] = useState("");
  const [staged, setStaged] = useState<Staged | null>(null);
  const [error, setError] = useState("");

  const [ticked, setTicked] = useState<ReadonlySet<string>>(new Set());
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [scope, setScope] = useState<InstallScope>("user");
  // Links are on unless turned off, so the choice holds when the scope, and
  // with it the set of harnesses that need one, changes.
  const [unlinked, setUnlinked] = useState<ReadonlySet<SkillHarness>>(new Set());
  const [replace, setReplace] = useState(false);
  const [installing, setInstalling] = useState(false);

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
      setReplace(false);
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

  const changeSource = () => {
    staging.release();
    setStaged(null);
    setError("");
  };

  const close = () => {
    if (installing) return;
    staging.release();
    onOpenChange(false);
  };

  const links = installLinks(setup, scope);
  const clashes = staged ? stagedClashes(staged.skills, ticked, scope) : [];
  const chosen = staged ? staged.skills.filter((s) => ticked.has(s.name)).map((s) => s.name) : [];
  const canInstall = chosen.length > 0 && (clashes.length === 0 || replace) && !installing;
  const where = installPath(scope, setup, projectRoot);

  const install = async () => {
    if (!staged || !canInstall) return;
    setInstalling(true);
    setError("");
    try {
      const res = await command<{ skills: Skill[] | null }>("install_staged", {
        ...scopeArgs,
        id: staged.id,
        skills: chosen,
        scope,
        link: links.filter((l) => l.direct || !unlinked.has(l.harness)).map((l) => l.harness),
        replace: clashes.length > 0 && replace,
      });
      // Placing the skills is what empties the holding folder.
      staging.forget();
      onInstalled(res.skills ?? [], scope);
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setInstalling(false);
    }
  };

  const toggle = <T,>(set: ReadonlySet<T>, item: T, on: boolean): ReadonlySet<T> => {
    const next = new Set(set);
    if (on) next.add(item);
    else next.delete(item);
    return next;
  };

  const allTicked = staged !== null && chosen.length === staged.skills.length;

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent
        fullscreenOnMobile
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-xl md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">Install skills</DialogTitle>
          <DialogDescription className="text-[12px]">
            The source is fetched into a holding folder first. Nothing is installed until you pick what to keep.
          </DialogDescription>
        </DialogHeader>

        {fetching ? (
          <div className="min-h-0 space-y-2 overflow-y-auto py-6 text-center" role="status">
            <p className="flex items-center justify-center gap-2 text-[13px]">
              {/* The line is the status; a second one inside it would be read twice. */}
              <Spinner role="presentation" aria-hidden className="text-primary size-4 shrink-0" />
              <span className="min-w-0 break-words">Fetching {fetching}</span>
            </p>
            <p className="text-muted-foreground text-[12px] leading-snug">
              This can take a minute. It carries on if your connection drops.
            </p>
          </div>
        ) : !staged ? (
          <form
            id="install-skills-form"
            className="scroll-thin -mx-1 min-h-0 space-y-2 overflow-y-auto px-1"
            onSubmit={(e) => {
              e.preventDefault();
              void fetchSource();
            }}
          >
            <Label htmlFor={sourceId}>Source</Label>
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
              aria-describedby={`${sourceId}-hint`}
              className="min-h-16 font-mono break-all"
              autoFocus
            />
            <p id={`${sourceId}-hint`} className="text-muted-foreground text-[12px] leading-snug">
              Paste owner/repo, a URL, a folder, or an npx skills add command.
            </p>
            {error && <ErrorLine message={error} />}
          </form>
        ) : (
          <div className="scroll-thin -mx-1 min-h-0 space-y-5 overflow-y-auto px-1">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <p className="min-w-0 flex-1 basis-40 text-[12.5px] leading-snug break-words">
                {staged.skills.length} {staged.skills.length === 1 ? "skill" : "skills"} in{" "}
                <span className="font-mono text-[12px]">{staged.repo}</span>
                {staged.ref && (
                  <>
                    {" "}
                    at <span className="font-mono text-[12px]">{staged.ref}</span>
                  </>
                )}
              </p>
              <Button
                variant="outline"
                size="sm"
                className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
                onClick={changeSource}
                disabled={installing}
              >
                Change source
              </Button>
            </div>
            {staged.note && <p className="text-muted-foreground -mt-3 text-[12px] leading-snug">{staged.note}</p>}

            <section aria-label="Skills to install">
              <div className="flex items-center gap-2">
                <SectionHeading className="mb-0 flex-1">Skills</SectionHeading>
                {staged.skills.length > 1 && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-11 text-[12.5px] md:h-8 md:text-[12px]"
                    disabled={installing}
                    onClick={() => setTicked(new Set(allTicked ? [] : staged.skills.map((s) => s.name)))}
                  >
                    {allTicked ? "Clear all" : "Select all"}
                  </Button>
                )}
              </div>
              <ul className="divide-y rounded-lg border">
                {staged.skills.map((s) => {
                  const isOpen = expanded.has(s.name);
                  const clash = scope === "user" ? s.inUser : s.inProject;
                  return (
                    <li key={s.name} className="px-3">
                      <div className="flex items-start gap-1">
                        <TickRow
                          checked={ticked.has(s.name)}
                          onChange={(on) => setTicked((t) => toggle(t, s.name, on))}
                          disabled={installing}
                          className="flex-1"
                        >
                          <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
                            <span className="min-w-0 font-mono text-[13px] break-all">{s.name}</span>
                            {s.manual && <Marker>manual</Marker>}
                            {clash && <Marker tone="attention">already installed</Marker>}
                          </span>
                          {s.description && (
                            <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">
                              {s.description}
                            </span>
                          )}
                          <ProblemText problem={s.problem} />
                        </TickRow>
                        <button
                          type="button"
                          aria-expanded={isOpen}
                          aria-label={`Preview ${s.name}`}
                          onClick={() => setExpanded((e) => toggle(e, s.name, !isOpen))}
                          className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex h-11 shrink-0 items-center gap-1 rounded-md px-2 text-[12px] outline-none focus-visible:ring-2"
                        >
                          Preview
                          <ChevronRightIcon className={cn("size-3.5 transition-transform", isOpen && "rotate-90")} />
                        </button>
                      </div>
                      {isOpen && <StagedPreview command={command} scopeArgs={scopeArgs} id={staged.id} skill={s} />}
                    </li>
                  );
                })}
              </ul>
            </section>

            <section>
              <SectionHeading>Where</SectionHeading>
              <div role="radiogroup" aria-label="Where" className="flex flex-col gap-1.5">
                {SCOPES.map((s) => {
                  const unavailable = s.id === "project" && !projectAvailable;
                  const picked = scope === s.id;
                  return (
                    <button
                      key={s.id}
                      type="button"
                      role="radio"
                      aria-checked={picked}
                      disabled={unavailable || installing}
                      onClick={() => {
                        setScope(s.id);
                        setReplace(false);
                      }}
                      className={cn(
                        "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2 disabled:opacity-50",
                        picked ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
                      )}
                    >
                      <span className="text-[13px] leading-tight">{s.label}</span>
                      <span className="text-muted-foreground font-mono text-[11px] leading-tight break-all">
                        {unavailable
                          ? "Open from a thread or project to install here"
                          : installPath(s.id, setup, projectRoot) ||
                            (s.id === "user" ? "Your personal library" : "This project's library")}
                      </span>
                    </button>
                  );
                })}
              </div>
            </section>

            {links.length > 0 && (
              <section aria-label="Harnesses">
                <SectionHeading>Harnesses that get it</SectionHeading>
                <ul className="divide-y rounded-lg border">
                  {links.map((link) => (
                    <li key={link.harness} className="px-3">
                      <TickRow
                        // A harness that reads the library has the skill the
                        // moment it lands; there is nothing to opt out of.
                        checked={link.direct || !unlinked.has(link.harness)}
                        disabled={link.direct || installing}
                        onChange={(on) => setUnlinked((u) => toggle(u, link.harness, !on))}
                      >
                        <span className="block text-[13px] leading-tight">{harnessLabel(link.harness)}</span>
                        <span className="text-muted-foreground block text-[12px] leading-snug break-all">
                          {link.text}
                        </span>
                      </TickRow>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {clashes.length > 0 && (
              // Pinned to the bottom of the scroll: it is why Install is off,
              // and a long list would otherwise leave it out of sight.
              <div className="bg-background sticky bottom-0 z-10 pt-1">
                <div className="bg-attention-surface rounded-lg border px-3 py-1">
                  <TickRow checked={replace} onChange={setReplace} disabled={installing}>
                    <span className="block text-[13px] leading-tight">Replace what is installed</span>
                    <span className="text-attention-foreground block text-[12px] leading-snug break-words">
                      {joinWords(clashes)} {clashes.length === 1 ? "is" : "are"} already in{" "}
                      {where ? <span className="font-mono text-[11.5px] break-all">{where}</span> : "that library"}.
                      Installing deletes {clashes.length === 1 ? "that copy" : "those copies"} first.
                    </span>
                  </TickRow>
                </div>
              </div>
            )}

            {error && <ErrorLine message={error} />}
          </div>
        )}

        <DialogFooter>
          {fetching ? (
            <Button variant="outline" onClick={cancelFetch}>
              Cancel
            </Button>
          ) : !staged ? (
            <>
              <Button variant="outline" onClick={close}>
                Cancel
              </Button>
              <Button type="submit" form="install-skills-form" disabled={!source.trim()}>
                Fetch
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" onClick={close} disabled={installing}>
                Cancel
              </Button>
              <Button onClick={() => void install()} disabled={!canInstall}>
                {installing && <Spinner className="size-3.5" />}
                {chosen.length > 1 ? `Install ${chosen.length} skills` : "Install"}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
