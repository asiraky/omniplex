import { ChevronRightIcon, PlusIcon, RefreshCwIcon, SearchIcon, Settings2Icon, XIcon } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Spinner } from "~/components/ui/spinner";
import {
  copiesOf,
  errorText,
  groupSkills,
  harnessLabel,
  matchesQuery,
  normalizeSkill,
  type Setup,
  type Skill,
  type SkillEntry,
  type SkillGroup,
  type SkillsList,
  type Subagent,
} from "~/lib/skills";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { InPromptView } from "./InPromptView";
import { NewSkillDialog } from "./NewSkillDialog";
import {
  ErrorLine,
  Marker,
  ProblemText,
  Segmented,
  SkillMarkers,
  type SkillsCommand,
  type SkillsContext,
  type SkillsSlots,
} from "./parts";
import { SkillDetailView } from "./SkillDetailView";
import { SkillsSetupDialog } from "./SkillsSetupDialog";

export type { SkillsCommand, SkillsContext, SkillsSlots } from "./parts";

export interface SkillsSurfaceProps {
  /** A ws command: list_skills, read_skill, save_skill, set_skill_invocation and the rest. */
  command: SkillsCommand;
  threadId?: string;
  projectId?: string;
  /**
   * Puts a skill's token into the thread's composer. Given only where there
   * is a thread to write into; without it the detail view offers no Use.
   */
  onUse?: (skill: Skill) => void | Promise<void>;
  /** Where the install, commit and update flows attach. */
  slots?: SkillsSlots;
}

type View = "skills" | "prompt";

const VIEWS: { id: View; label: string }[] = [
  { id: "skills", label: "Skills" },
  { id: "prompt", label: "In prompt" },
];

/** Name, two lines of description, and only the markers that are exceptions. */
function SkillRow({ entry, onOpen }: { entry: SkillEntry; onOpen: (skill: Skill) => void }) {
  const skill = entry.copies[0];
  // A problem on any copy is worth the row's attention, not just the first's.
  const problem = skill.problem ?? entry.copies.find((c) => c.problem)?.problem;
  return (
    <button
      type="button"
      onClick={() => onOpen(skill)}
      className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full flex-col justify-center gap-0.5 rounded-md px-2 py-2 text-left transition-colors outline-none focus-visible:ring-2"
    >
      <span className="flex w-full min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
        <span className="min-w-0 truncate font-mono text-[13px]">{skill.name}</span>
        {entry.copies.length > 1 && <Marker>{entry.copies.length} copies</Marker>}
        <SkillMarkers skill={skill} />
      </span>
      {skill.description && (
        <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">{skill.description}</span>
      )}
      <ProblemText problem={problem} />
    </button>
  );
}

function EntryList({ entries, onOpen }: { entries: SkillEntry[]; onOpen: (skill: Skill) => void }) {
  return (
    <ul>
      {entries.map((entry) => (
        <li key={entry.copies[0].dir}>
          <SkillRow entry={entry} onOpen={onOpen} />
        </li>
      ))}
    </ul>
  );
}

const groupTriggerClass =
  "text-muted-foreground hover:text-foreground focus-visible:ring-ring group flex min-h-11 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-[11px] font-semibold tracking-wide uppercase outline-none focus-visible:ring-2 md:min-h-8";

function GroupSection({
  group,
  open,
  onOpenChange,
  onOpen,
  action,
}: {
  group: SkillGroup;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpen: (skill: Skill) => void;
  /** Sits at the right end of the header, outside the fold control. */
  action?: ReactNode;
}) {
  return (
    <section aria-label={group.title} className="mb-1">
      <Collapsible open={open} onOpenChange={onOpenChange}>
        <div className="flex items-center gap-1">
          <CollapsibleTrigger className={groupTriggerClass}>
            <ChevronRightIcon className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            {/* A repo is a name, not a heading: it keeps its own case. */}
            <span className={cn("truncate", group.kind === "source" && "font-mono tracking-normal normal-case")}>
              {group.title}
            </span>
            <span className="tabular-nums">{group.entries.length}</span>
          </CollapsibleTrigger>
          {action}
        </div>
        <CollapsibleContent>
          {group.subgroups ? (
            group.subgroups.map((sub) => (
              <section key={sub.key} aria-label={sub.title} className="mb-1">
                <h4 className="text-muted-foreground flex items-center gap-1.5 px-2 pt-2 pb-0.5 text-[11.5px] font-medium">
                  <span className="truncate">{sub.title}</span>
                  <span className="tabular-nums">{sub.entries.length}</span>
                </h4>
                <EntryList entries={sub.entries} onOpen={onOpen} />
              </section>
            ))
          ) : (
            <EntryList entries={group.entries} onOpen={onOpen} />
          )}
        </CollapsibleContent>
      </Collapsible>
    </section>
  );
}

function SubagentRow({ agent }: { agent: Subagent }) {
  return (
    <li className="px-2 py-1.5">
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{agent.name}</span>
        <span className="text-muted-foreground shrink-0 text-[11px]">
          {harnessLabel(agent.harness)} · {agent.scope === "project" ? "project" : "personal"}
        </span>
      </div>
      {agent.description && (
        <p className="text-muted-foreground line-clamp-2 text-[12px] leading-snug">{agent.description}</p>
      )}
    </li>
  );
}

/**
 * The skills browser: every Agent Skill the harnesses can see for this scope,
 * grouped by where it came from, with what each harness does with it and an
 * editor for the ones that are ours to edit. Fetched once on open and again
 * only on an explicit refresh or after a change that moves things on disk.
 */
export function SkillsSurface({ command, threadId, projectId, onUse, slots }: SkillsSurfaceProps) {
  const scopeArgs = useMemo(() => {
    const args: Record<string, unknown> = {};
    if (threadId) args.threadId = threadId;
    if (projectId) args.projectId = projectId;
    return args;
  }, [threadId, projectId]);

  const [list, setList] = useState<SkillsList | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  const [query, setQuery] = useState("");
  const [view, setView] = useState<View>("skills");
  // Which groups the user folded or unfolded, over each group's default. A
  // search has its own set: it opens every group with a match, and what is
  // folded while searching should not outlive the search.
  const [folds, setFolds] = useState<Record<string, boolean>>({});
  const [searchFolds, setSearchFolds] = useState<Record<string, boolean>>({});
  const [open, setOpen] = useState<{ skill: Skill; edit: boolean } | null>(null);
  const [creating, setCreating] = useState(false);
  // Bumped on every opening, so each dialog mounts with a fresh form.
  const [createSeq, setCreateSeq] = useState(0);
  const [settingUp, setSettingUp] = useState(false);
  const [setupSeq, setSetupSeq] = useState(0);

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
        setList({ ...l, skills: (l.skills ?? []).map(normalizeSkill), subagents: l.subagents ?? [] });
        setLoading(false);
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

  const skills = useMemo(() => list?.skills ?? [], [list]);
  const searching = query.trim() !== "";
  // Copies are found in the whole list, not the searched one: a search that
  // matches one copy's description must not hide the other from the detail.
  const allGroups = useMemo(() => groupSkills(skills), [skills]);
  const groups = useMemo(
    () => (searching ? groupSkills(skills.filter((s) => matchesQuery(s, query))) : allGroups),
    [skills, query, searching, allGroups],
  );
  const subagents = list?.subagents ?? [];
  const projectAvailable = Boolean(threadId || projectId);

  const changeQuery = (next: string) => {
    setQuery(next);
    setSearchFolds({});
  };

  const groupOpen = (group: SkillGroup) => (searching ? (searchFolds[group.key] ?? true) : (folds[group.key] ?? !group.collapsed));
  const setGroupOpen = (group: SkillGroup, next: boolean) =>
    (searching ? setSearchFolds : setFolds)((f) => ({ ...f, [group.key]: next }));

  const openSkill = (skill: Skill, edit = false) => {
    if (!open) savedScroll.current = listScrollRef.current?.scrollTop ?? 0;
    setOpen({ skill, edit });
  };

  // The list stays mounted under the detail view, but a hidden element can
  // come back scrolled to the top; put the reader back where they were.
  useLayoutEffect(() => {
    if (!open && listScrollRef.current) listScrollRef.current.scrollTop = savedScroll.current;
  }, [open]);

  const upsert = useCallback((incoming: Skill[]) => {
    const next = incoming.map(normalizeSkill);
    setList((l) => {
      if (!l) return l;
      const byDir = new Map(next.map((s) => [s.dir, s]));
      const kept = l.skills.map((s) => byDir.get(s.dir) ?? s);
      const known = new Set(l.skills.map((s) => s.dir));
      return { ...l, skills: [...kept, ...next.filter((s) => !known.has(s.dir))] };
    });
    setOpen((o) => {
      const fresh = o && next.find((s) => s.dir === o.skill.dir);
      return o && fresh ? { ...o, skill: fresh } : o;
    });
  }, []);

  const removed = useCallback((gone: Skill) => {
    setList((l) => (l ? { ...l, skills: l.skills.filter((s) => s.dir !== gone.dir) } : l));
    setOpen(null);
  }, []);

  const changeSetup = (setup: Setup) => {
    setList((l) => (l ? { ...l, setup } : l));
    // A moved library or a new link changes which skills exist and who sees them.
    refresh();
  };

  const ctx: SkillsContext = {
    command,
    scopeArgs,
    list,
    setup: list?.setup,
    projectAvailable,
    refresh,
    upsert,
  };

  const empty = !loading && !error && list !== null && skills.length === 0;
  const noMatches = !empty && list !== null && groups.length === 0;
  const commitBar = slots?.commitBar?.(ctx);

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
        <div className="border-b px-2 py-1">
          <div className="flex items-center gap-1">
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
            <IconButton label="Refresh skills" onClick={refresh} disabled={loading}>
              <RefreshCwIcon className={cn(loading && "animate-spin")} />
            </IconButton>
            <IconButton
              label="Skills setup"
              onClick={() => {
                setSetupSeq((n) => n + 1);
                setSettingUp(true);
              }}
              disabled={!list}
            >
              <Settings2Icon />
            </IconButton>
          </div>
          {/* A second row rather than one crowded one: the panel is as narrow
              as a phone, and four controls beside a search box leave no box. */}
          <div className="flex flex-wrap items-center gap-1.5 pt-1 pb-0.5">
            <Segmented label="Skills views" value={view} onChange={setView} options={VIEWS} className="mr-auto" />
            {slots?.install?.(ctx)}
            <Button
              size="sm"
              className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]"
              onClick={() => {
                setCreateSeq((n) => n + 1);
                setCreating(true);
              }}
            >
              <PlusIcon className="size-3.5" />
              New skill
            </Button>
          </div>
        </div>

        {commitBar}

        <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
          {error && (
            <div className="space-y-2 px-2 py-3">
              <ErrorLine message={`Could not list skills: ${error}`} />
              <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={refresh}>
                Try again
              </Button>
            </div>
          )}
          {loading && !list && (
            <p className="text-muted-foreground flex items-center justify-center gap-2 px-2 py-10 text-[12.5px]">
              <Spinner className="text-primary size-3.5" /> Finding skills…
            </p>
          )}
          {empty && (
            <div className="text-muted-foreground px-4 py-10 text-center text-[12.5px]">
              <p>No skills yet.</p>
              <p className="mt-1">Create one to start.</p>
            </div>
          )}

          {view === "prompt" && list && !empty && <InPromptView skills={skills} query={query} onOpen={openSkill} />}

          {view === "skills" && (
            <>
              {noMatches && (
                <p className="text-muted-foreground px-2 py-10 text-center text-[12.5px]">
                  No skills match “{query.trim()}”.
                </p>
              )}

              {groups.map((group) => (
                <GroupSection
                  key={group.key}
                  group={group}
                  open={groupOpen(group)}
                  onOpenChange={(next) => setGroupOpen(group, next)}
                  onOpen={openSkill}
                  action={group.kind === "source" ? slots?.groupAction?.(group, ctx) : undefined}
                />
              ))}

              {subagents.length > 0 && !searching && (
                <Collapsible className="mt-2 border-t pt-1">
                  <CollapsibleTrigger className={cn(groupTriggerClass, "w-full")}>
                    <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-90" />
                    Subagents
                    <span className="tabular-nums">{subagents.length}</span>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <ul className="pb-2">
                      {subagents.map((a) => (
                        <SubagentRow key={a.path} agent={a} />
                      ))}
                    </ul>
                  </CollapsibleContent>
                </Collapsible>
              )}
            </>
          )}
        </div>
      </div>

      {open && (
        <SkillDetailView
          key={open.skill.dir}
          command={command}
          scopeArgs={scopeArgs}
          skill={open.skill}
          copies={copiesOf(allGroups, open.skill.dir)}
          startEditing={open.edit}
          onBack={() => setOpen(null)}
          onChanged={(next) => upsert([next])}
          onRemoved={removed}
          onSwitchCopy={(copy) => setOpen({ skill: copy, edit: false })}
          onUse={onUse}
          updateAction={open.skill.source ? slots?.update?.(open.skill, ctx) : undefined}
        />
      )}

      <NewSkillDialog
        key={`new:${createSeq}:${projectAvailable}`}
        open={creating}
        onOpenChange={setCreating}
        command={command}
        scopeArgs={scopeArgs}
        projectAvailable={projectAvailable}
        setup={list?.setup}
        onCreated={(skill) => {
          upsert([skill]);
          openSkill(normalizeSkill(skill), true);
        }}
      />

      <SkillsSetupDialog
        key={`setup:${setupSeq}`}
        open={settingUp}
        onOpenChange={setSettingUp}
        command={command}
        scopeArgs={scopeArgs}
        setup={list?.setup}
        onSetup={changeSetup}
      />
    </div>
  );
}
