import { ChevronRightIcon, PlusIcon, RefreshCwIcon, SearchIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Spinner } from "~/components/ui/spinner";
import {
  groupSkills,
  HARNESSES,
  matchesFilter,
  matchesQuery,
  type Skill,
  type SkillFilter,
  type SkillsList,
  type Subagent,
} from "~/lib/skills";
import { cn } from "~/lib/utils";

import { NewSkillDialog } from "./NewSkillDialog";
import { ErrorLine, errorText, HarnessChips, ProblemIcon, Segmented, type SkillsCommand } from "./parts";
import { SkillDetailView } from "./SkillDetailView";

export type { SkillsCommand } from "./parts";

export interface SkillsSurfaceProps {
  /** A ws command: list_skills, read_skill, read_skill_file, save_skill, create_skill. */
  command: SkillsCommand;
  threadId?: string;
  projectId?: string;
}

const FILTERS: { id: SkillFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "project", label: "Project" },
  { id: "user", label: "Personal" },
  { id: "plugin", label: "Plugins" },
];

function SkillRow({ skill, onOpen }: { skill: Skill; onOpen: (skill: Skill) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(skill)}
      className="hover:bg-accent/50 focus-visible:ring-ring group flex min-h-11 w-full flex-col gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors outline-none focus-visible:ring-2"
    >
      <span className="flex w-full min-w-0 items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{skill.name}</span>
        <ProblemIcon problem={skill.problem} />
        <HarnessChips harnesses={skill.harnesses} />
      </span>
      {skill.description && (
        <span className="text-muted-foreground line-clamp-2 text-[11.5px] leading-snug">{skill.description}</span>
      )}
    </button>
  );
}

function SubagentRow({ agent }: { agent: Subagent }) {
  const harness = HARNESSES.find((h) => h.id === agent.harness)?.label ?? agent.harness;
  return (
    <li className="px-2 py-1.5" title={agent.path}>
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[11.5px]">{agent.name}</span>
        <span className="text-muted-foreground shrink-0 text-[10px]">
          {harness} · {agent.scope === "project" ? "project" : "personal"}
        </span>
      </div>
      {agent.description && (
        <p className="text-muted-foreground line-clamp-2 text-[11px] leading-snug">{agent.description}</p>
      )}
    </li>
  );
}

/**
 * The skills browser: every Agent Skill the harnesses can see for this
 * thread's project and for the user, which harness sees which, and an editor
 * for the ones that are ours to edit. Fetched once on open and again only on
 * an explicit refresh — nothing here changes often enough to poll for.
 */
export function SkillsSurface({ command, threadId, projectId }: SkillsSurfaceProps) {
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
  const [filter, setFilter] = useState<SkillFilter>("all");
  const [open, setOpen] = useState<{ skill: Skill; edit: boolean } | null>(null);
  const [creating, setCreating] = useState(false);

  const listScrollRef = useRef<HTMLDivElement>(null);
  const savedScroll = useRef(0);

  const commandRef = useRef(command);
  commandRef.current = command;

  useEffect(() => {
    let stale = false;
    setLoading(true);
    setError("");
    commandRef
      .current<SkillsList>("list_skills", scopeArgs)
      .then((l) => {
        if (stale) return;
        setList(l);
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
  }, [scopeArgs, refreshSeq]);

  const skills = list?.skills ?? [];
  const searched = useMemo(() => skills.filter((s) => matchesQuery(s, query)), [skills, query]);
  const counts = useMemo(() => {
    const c: Record<SkillFilter, number> = { all: 0, project: 0, user: 0, plugin: 0 };
    for (const s of searched) {
      c.all++;
      if (matchesFilter(s, "project")) c.project++;
      if (matchesFilter(s, "user")) c.user++;
      if (matchesFilter(s, "plugin")) c.plugin++;
    }
    return c;
  }, [searched]);
  const sections = useMemo(
    () => groupSkills(searched.filter((s) => matchesFilter(s, filter))),
    [searched, filter],
  );
  const subagents = list?.subagents ?? [];
  const projectAvailable = Boolean(threadId || projectId);

  const openSkill = (skill: Skill, edit = false) => {
    savedScroll.current = listScrollRef.current?.scrollTop ?? 0;
    setOpen({ skill, edit });
  };

  // The list stays mounted under the detail view, but a hidden element can
  // come back scrolled to the top; put the reader back where they were.
  useLayoutEffect(() => {
    if (!open && listScrollRef.current) listScrollRef.current.scrollTop = savedScroll.current;
  }, [open]);

  const replaceSkill = useCallback((next: Skill) => {
    setList((l) => {
      if (!l) return l;
      const i = l.skills.findIndex((s) => s.dir === next.dir);
      const skills = i >= 0 ? l.skills.map((s, j) => (j === i ? next : s)) : [...l.skills, next];
      return { ...l, skills };
    });
    setOpen((o) => (o && o.skill.dir === next.dir ? { ...o, skill: next } : o));
  }, []);

  const empty = !loading && !error && list !== null && skills.length === 0;
  const noMatches = !empty && list !== null && sections.length === 0;

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
        <div className="flex items-center gap-1 border-b px-2 py-1">
          <div className="relative min-w-0 flex-1">
            <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search skills"
              aria-label="Search skills"
              className="placeholder:text-muted-foreground focus-visible:ring-ring h-11 w-full rounded-md bg-transparent pr-7 pl-7 text-base outline-none focus-visible:ring-2 md:h-8 md:text-[12px] [&::-webkit-search-cancel-button]:hidden"
            />
            {query && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => setQuery("")}
                className="text-muted-foreground hover:text-foreground absolute top-1/2 right-0 flex size-11 -translate-y-1/2 items-center justify-center md:size-8"
              >
                <XIcon className="size-3.5" />
              </button>
            )}
          </div>
          <IconButton label="Refresh skills" onClick={() => setRefreshSeq((n) => n + 1)} disabled={loading}>
            <RefreshCwIcon className={cn(loading && "animate-spin")} />
          </IconButton>
          <Button size="sm" className="h-11 shrink-0 text-[12px] md:h-8" onClick={() => setCreating(true)}>
            <PlusIcon className="size-3.5" />
            New skill
          </Button>
        </div>

        <div className="border-b px-2 py-1.5">
          <Segmented
            label="Filter skills"
            value={filter}
            onChange={setFilter}
            options={FILTERS.map((f) => ({ ...f, count: list ? counts[f.id] : undefined }))}
          />
        </div>

        <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
          {error && (
            <div className="space-y-2 px-2 py-3">
              <ErrorLine message={`Could not list skills: ${error}`} />
              <Button variant="outline" size="sm" onClick={() => setRefreshSeq((n) => n + 1)}>
                Try again
              </Button>
            </div>
          )}
          {loading && !list && (
            <p className="text-muted-foreground flex items-center justify-center gap-2 px-2 py-10 text-[12px]">
              <Spinner className="text-primary size-3.5" /> Finding skills…
            </p>
          )}
          {empty && (
            <div className="text-muted-foreground px-4 py-10 text-center text-[12px]">
              <p>No skills yet.</p>
              <p className="mt-1">Skills live in .agents/skills and .claude/skills; create one to start.</p>
            </div>
          )}
          {noMatches && (
            <p className="text-muted-foreground px-2 py-10 text-center text-[12px]">
              {query ? `No skills match “${query.trim()}”.` : "No skills here."}
            </p>
          )}

          {sections.map((section) => (
            <section key={section.key} aria-label={section.title} className="mb-2">
              <h3 className="text-muted-foreground flex items-center gap-1.5 px-2 pt-2 pb-1 text-[10px] font-semibold tracking-wide uppercase">
                <span className="truncate">{section.title}</span>
                <span className="tabular-nums">{section.skills.length}</span>
              </h3>
              <ul>
                {section.skills.map((skill) => (
                  <li key={skill.dir}>
                    <SkillRow skill={skill} onOpen={openSkill} />
                  </li>
                ))}
              </ul>
            </section>
          ))}

          {subagents.length > 0 && (
            <Collapsible className="mt-2 border-t pt-1">
              <CollapsibleTrigger className="text-muted-foreground hover:text-foreground focus-visible:ring-ring group flex min-h-11 w-full items-center gap-1.5 rounded-md px-2 text-[10px] font-semibold tracking-wide uppercase outline-none focus-visible:ring-2 md:min-h-8">
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
        </div>
      </div>

      {open && (
        <SkillDetailView
          key={open.skill.dir}
          command={command}
          scopeArgs={scopeArgs}
          skill={open.skill}
          startEditing={open.edit}
          onBack={() => setOpen(null)}
          onChanged={replaceSkill}
        />
      )}

      <NewSkillDialog
        open={creating}
        onOpenChange={setCreating}
        command={command}
        scopeArgs={scopeArgs}
        projectAvailable={projectAvailable}
        onCreated={(skill) => {
          replaceSkill(skill);
          openSkill(skill, true);
        }}
      />
    </div>
  );
}
