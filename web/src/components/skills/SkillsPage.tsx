import { ChevronRightIcon, PlusIcon, SearchIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import {
  errorText,
  matchesQuery,
  SECTION_ORDER,
  sectionSkills,
  type SectionKind,
  type Skill,
  type SkillsList,
  type SkillsScope,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { AddSheet } from "./AddSheet";
import { CommitStrip } from "./CommitStrip";
import { ErrorLine, ModeChip, ProblemText, type SkillsCommand } from "./parts";
import { SkillDetailView } from "./SkillDetailView";

/** Sections that are not ours to edit start folded; the reader came for their own. */
const FOLDED: Record<SectionKind, boolean> = {
  yours: false,
  project: false,
  synced: true,
  system: true,
  plugins: true,
};

const baseName = (path: string) => path.replace(/[/\\]+$/, "").split(/[/\\]/).pop() ?? path;

/** Name, two lines of description, a chip only when the skill is not on. */
function SkillRow({ skill, showPlugin, onOpen }: { skill: Skill; showPlugin: boolean; onOpen: (skill: Skill) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(skill)}
      className={cn(
        "hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full flex-col justify-center gap-0.5 rounded-md px-2 py-2 text-left transition-colors outline-none focus-visible:ring-2",
        skill.mode === "off" && "opacity-60",
      )}
    >
      <span className="flex w-full min-w-0 items-center gap-1.5">
        {/* The spaces are for the button's spoken name; flex drops them on screen. */}
        <span className="min-w-0 truncate font-mono text-[13px]">{skill.name}</span>{" "}
        {showPlugin && skill.plugin && (
          <span className="text-muted-foreground min-w-0 truncate text-[11.5px]">{skill.plugin}</span>
        )}{" "}
        <span className="ml-auto" />
        <ModeChip mode={skill.mode} />
      </span>{" "}
      {skill.description && (
        <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">{skill.description}</span>
      )}
      <ProblemText problem={skill.problem} />
    </button>
  );
}

/**
 * An agent's own on/off for a whole set of skills, in its section's header.
 * The switch moves at once and goes back if the write fails.
 */
function SectionSwitch({
  label,
  on,
  onChange,
}: {
  label: string;
  on: boolean;
  onChange: (on: boolean) => Promise<void>;
}) {
  const [pending, setPending] = useState<boolean | null>(null);
  const [error, setError] = useState("");
  const change = (next: boolean) => {
    setPending(next);
    setError("");
    onChange(next)
      .catch((e) => setError(errorText(e)))
      .finally(() => setPending(null));
  };
  return (
    <>
      <span className="flex shrink-0 items-center gap-2 px-2">
        {pending !== null && <Spinner className="size-3.5" />}
        {/* The 44px target is the label around it; the switch is drawn small. */}
        <label className="flex min-h-11 cursor-pointer items-center md:min-h-8">
          <Switch checked={pending ?? on} disabled={pending !== null} onCheckedChange={change} aria-label={label} />
        </label>
      </span>
      {error && <ErrorLine message={error} className="basis-full px-2 pb-1" />}
    </>
  );
}

function Section({
  title,
  count,
  open,
  onOpenChange,
  action,
  note,
  children,
}: {
  title: string;
  count: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Sits at the right end of the header, outside the fold control. */
  action?: ReactNode;
  /** A line under the header, shown folded or not. */
  note?: string;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="mb-1">
      <Collapsible open={open} onOpenChange={onOpenChange}>
        <div className="flex flex-wrap items-center">
          <CollapsibleTrigger className="text-muted-foreground hover:text-foreground focus-visible:ring-ring group flex min-h-11 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-[11px] font-semibold tracking-wide uppercase outline-none focus-visible:ring-2 md:min-h-8">
            <ChevronRightIcon className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            <span className="truncate">{title}</span>
            {count > 0 && <span className="tabular-nums">{count}</span>}
          </CollapsibleTrigger>
          {action}
        </div>
        {note && <p className="text-muted-foreground px-2 pb-1 text-[12.5px] leading-snug">{note}</p>}
        <CollapsibleContent>{children}</CollapsibleContent>
      </Collapsible>
    </section>
  );
}

/**
 * The Skills page: every skill the agents can see from here, in fixed
 * sections, with an editor for the ones that are ours. Read again on open and
 * after every change.
 */
export function SkillsPage({
  command,
  scope,
  onClose,
}: {
  command: SkillsCommand;
  scope: SkillsScope;
  onClose: () => void;
}) {
  const scopeArgs = useMemo(() => {
    const args: Record<string, unknown> = {};
    if (scope.threadId) args.threadId = scope.threadId;
    else if (scope.projectId) args.projectId = scope.projectId;
    return args;
  }, [scope.threadId, scope.projectId]);

  const [list, setList] = useState<SkillsList | null>(null);
  // Counts the answers from list_skills: each one asks git again.
  const [loads, setLoads] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  const [query, setQuery] = useState("");
  // Folds the reader chose, over each section's default. A search opens every
  // section with a match, and its folds do not outlive it.
  const [folds, setFolds] = useState<Partial<Record<SectionKind, boolean>>>({});
  const [searchFolds, setSearchFolds] = useState<Partial<Record<SectionKind, boolean>>>({});
  const [open, setOpen] = useState<{ skill: Skill; edit: boolean } | null>(null);
  const [adding, setAdding] = useState(false);
  // Bumped on every opening, so each sheet starts at the paste box.
  const [addSeq, setAddSeq] = useState(0);

  const listScrollRef = useRef<HTMLDivElement>(null);
  const savedScroll = useRef(0);
  const commandRef = useLatest(command);

  useEffect(() => {
    let stale = false;
    setLoading(true);
    setError("");
    commandRef
      .current<SkillsList>("list_skills", scopeArgs)
      .then((l) => {
        if (stale) return;
        const skills = l.skills ?? [];
        setList({ ...l, skills });
        setLoads((n) => n + 1);
        setLoading(false);
        // The open skill follows the list: a write elsewhere can change its mode.
        setOpen((o) => {
          const fresh = o && skills.find((s) => s.dir === o.skill.dir);
          return o && fresh ? { ...o, skill: fresh } : o;
        });
      })
      .catch((e) => {
        if (stale) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => {
      stale = true;
    };
  }, [scopeArgs, refreshSeq, commandRef]);

  const refresh = useCallback(() => setRefreshSeq((n) => n + 1), []);

  /** Shows what a write returned at once, then reads the whole list again. */
  const upsert = useCallback(
    (incoming: Skill[]) => {
      setList((l) => {
        if (!l) return l;
        const byDir = new Map(incoming.map((s) => [s.dir, s]));
        const kept = l.skills.map((s) => byDir.get(s.dir) ?? s);
        const known = new Set(l.skills.map((s) => s.dir));
        return { ...l, skills: [...kept, ...incoming.filter((s) => !known.has(s.dir))] };
      });
      setOpen((o) => {
        const fresh = o && incoming.find((s) => s.dir === o.skill.dir);
        return o && fresh ? { ...o, skill: fresh } : o;
      });
      refresh();
    },
    [refresh],
  );

  const removed = useCallback(
    (gone: Skill) => {
      setList((l) => (l ? { ...l, skills: l.skills.filter((s) => s.dir !== gone.dir) } : l));
      setOpen(null);
      refresh();
    },
    [refresh],
  );

  const openSkill = (skill: Skill, edit = false) => {
    if (!open) savedScroll.current = listScrollRef.current?.scrollTop ?? 0;
    setOpen({ skill, edit });
  };

  // The list stays mounted under the detail view, but a hidden element can
  // come back scrolled to the top; put the reader back where they were.
  useLayoutEffect(() => {
    if (!open && listScrollRef.current) listScrollRef.current.scrollTop = savedScroll.current;
  }, [open]);

  const setClaudeSync = async (on: boolean) => {
    const res = await command<{ claudeSync: boolean }>("set_claude_sync", { ...scopeArgs, on });
    setList((l) => (l ? { ...l, claudeSync: res.claudeSync } : l));
    refresh();
  };

  const setCodexBundled = async (on: boolean) => {
    const res = await command<{ codexBundled: boolean }>("set_codex_bundled", { ...scopeArgs, on });
    setList((l) => (l ? { ...l, codexBundled: res.codexBundled } : l));
    refresh();
  };

  const searching = query.trim() !== "";
  const sections = useMemo(
    () => sectionSkills((list?.skills ?? []).filter((s) => matchesQuery(s, query))),
    [list, query],
  );

  const changeQuery = (next: string) => {
    setQuery(next);
    setSearchFolds({});
  };

  const sectionOpen = (kind: SectionKind) =>
    searching ? (searchFolds[kind] ?? true) : (folds[kind] ?? !FOLDED[kind]);
  const setSectionOpen = (kind: SectionKind, next: boolean) =>
    (searching ? setSearchFolds : setFolds)((f) => ({ ...f, [kind]: next }));

  const projectName =
    list?.projectName || scope.projectName || (list?.projectRoot ? baseName(list.projectRoot) : "");

  const renderSection = (kind: SectionKind) => {
    if (!list) return null;
    const skills = sections[kind];
    // A search shows only where it found something.
    if (searching && skills.length === 0) return null;

    let title: string;
    let action: ReactNode;
    let note: string | undefined;
    switch (kind) {
      case "yours":
        title = "Yours";
        if (skills.length === 0) note = "No skills yet. Add one to start.";
        break;
      case "project":
        if (!list.projectRoot || skills.length === 0) return null;
        title = projectName ? `Project: ${projectName}` : "Project";
        break;
      case "synced":
        title = "From claude.ai";
        action = <SectionSwitch label="Sync from claude.ai" on={list.claudeSync} onChange={setClaudeSync} />;
        if (!searching) {
          note = !list.claudeSync ? "Off. Claude won't load these." : skills.length === 0 ? "None synced yet." : undefined;
        }
        break;
      case "system":
        title = "Codex built-in";
        action = <SectionSwitch label="Codex built-in skills" on={list.codexBundled} onChange={setCodexBundled} />;
        if (!searching) {
          note = !list.codexBundled ? "Off. Codex won't load these." : skills.length === 0 ? "None found." : undefined;
        }
        break;
      case "plugins":
        if (skills.length === 0) return null;
        title = "Plugins";
        break;
    }

    return (
      <Section
        key={kind}
        title={title}
        count={skills.length}
        open={sectionOpen(kind)}
        onOpenChange={(next) => setSectionOpen(kind, next)}
        action={action}
        note={note}
      >
        {skills.length > 0 && (
          <ul>
            {skills.map((skill) => (
              <li key={skill.dir}>
                <SkillRow skill={skill} showPlugin={kind === "plugins"} onOpen={(s) => openSkill(s)} />
              </li>
            ))}
          </ul>
        )}
      </Section>
    );
  };

  const rendered = list ? SECTION_ORDER.map(renderSection) : [];
  const noMatches = searching && list !== null && rendered.every((r) => r === null);

  return (
    <div className="bg-background fixed inset-0 z-50 flex flex-col pb-[env(safe-area-inset-bottom)]">
      <header className="flex items-center gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-4">
        <IconButton label="Close skills" onClick={onClose}>
          <XIcon />
        </IconButton>
        <h1 className="min-w-0 flex-1 text-[15px] leading-tight font-semibold">Skills</h1>
      </header>
      <div className="min-h-0 flex-1 border-t">
        {/* A list of names and sentences: past this width the lines only get
            harder to follow. */}
        <div className="relative mx-auto flex h-full w-full max-w-3xl min-h-0 flex-col">
          <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
            <div className="flex items-center gap-1.5 border-b px-2 py-1">
              <div className="relative min-w-0 flex-1">
                <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
                <input
                  type="search"
                  value={query}
                  onChange={(e) => changeQuery(e.target.value)}
                  placeholder="Search skills"
                  aria-label="Search skills"
                  className="placeholder:text-muted-foreground focus-visible:ring-ring h-11 w-full rounded-md bg-transparent pr-7 pl-7 text-base outline-none focus-visible:ring-2 md:h-8 md:text-[12px] [&::-webkit-search-cancel-button]:hidden"
                />
                {query && (
                  <button
                    type="button"
                    aria-label="Clear search"
                    onClick={() => changeQuery("")}
                    className="text-muted-foreground hover:text-foreground absolute top-1/2 right-0 flex size-11 -translate-y-1/2 items-center justify-center md:size-8"
                  >
                    <XIcon className="size-3.5" />
                  </button>
                )}
              </div>
              <Button
                size="sm"
                className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]"
                onClick={() => {
                  setAddSeq((n) => n + 1);
                  setAdding(true);
                }}
              >
                <PlusIcon className="size-3.5" />
                Add
              </Button>
            </div>

            <CommitStrip command={command} scopeArgs={scopeArgs} version={loads} />

            <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
              {error && (
                <div className="space-y-2 px-2 py-3">
                  <ErrorLine message={`Could not list skills. ${error}`} />
                  <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={refresh} disabled={loading}>
                    Try again
                  </Button>
                </div>
              )}
              {loading && !list && (
                <p className="text-muted-foreground flex items-center justify-center gap-2 px-2 py-10 text-[12.5px]">
                  <Spinner className="text-primary size-3.5" /> Finding skills…
                </p>
              )}
              {noMatches && (
                <p className="text-muted-foreground px-2 py-10 text-center text-[12.5px]">
                  No skills match “{query.trim()}”.
                </p>
              )}
              {rendered}
            </div>
          </div>

          {open && (
            <div className="absolute inset-0">
              <SkillDetailView
                key={open.skill.dir}
                command={command}
                scopeArgs={scopeArgs}
                skill={open.skill}
                startEditing={open.edit}
                onBack={() => setOpen(null)}
                onChanged={upsert}
                onRemoved={removed}
              />
            </div>
          )}
        </div>
      </div>

      <AddSheet
        key={addSeq}
        open={adding}
        onOpenChange={setAdding}
        command={command}
        scopeArgs={scopeArgs}
        onInstalled={upsert}
        onCreated={(skill) => {
          upsert([skill]);
          openSkill(skill, true);
        }}
      />
    </div>
  );
}
