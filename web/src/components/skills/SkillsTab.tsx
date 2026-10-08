import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import {
  ErrorLine,
  ListRow,
  ListToolbar,
  Loading,
  LoadError,
  Marker,
  RowSwitch,
  Section,
  type PageCommand,
} from "~/components/tools/parts";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import {
  destinationsOf,
  folderLabel,
  matchesQuery,
  PROJECT_LABEL,
  repoSectionTitle,
  SECTION_ORDER,
  sectionOf,
  sectionSkills,
  type ClaudeBuiltin,
  type SectionKind,
  type Skill,
  type SkillMode,
  type SkillsList,
  type SkillsScope,
} from "~/lib/skills";
import { cn, errorText } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { AddSheet } from "./AddSheet";
import { CommitStrip } from "./CommitStrip";
import { ModeChip } from "./parts";
import { SkillDetailView } from "./SkillDetailView";

/** Sections that are not ours to edit start folded; the reader came for their own. */
const FOLDED: Record<SectionKind, boolean> = {
  yours: false,
  private: false,
  project: false,
  synced: true,
  omniplex: true,
  claude: true,
  system: true,
  plugins: true,
};

const baseName = (path: string) => path.replace(/[/\\]+$/, "").split(/[/\\]/).pop() ?? path;

/** Name, two lines of description, chips only for what is out of the ordinary. */
function SkillRow({ skill, aside, onOpen }: { skill: Skill; aside?: string; onOpen: (skill: Skill) => void }) {
  return (
    <ListRow
      title={skill.name}
      aside={aside}
      markers={
        <>
          {skill.uncommitted && <Marker tone="attention">Not committed</Marker>}
          <ModeChip mode={skill.mode} />
        </>
      }
      sub={skill.description}
      problem={skill.problem}
      dim={skill.mode === "off"}
      onOpen={() => onOpen(skill)}
    />
  );
}

/**
 * A skill an agent ships with: on or off at the end of its row. The switch
 * moves at once and goes back if the write fails. With the whole set off it
 * shows off and stays put, and each skill's own switch comes back with the set.
 */
function BuiltinRow({
  name,
  description,
  mode,
  groupOn,
  problem,
  onOpen,
  onChange,
}: {
  name: string;
  description?: string;
  mode: SkillMode;
  groupOn: boolean;
  problem?: string;
  onOpen?: () => void;
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
  const on = pending ?? mode !== "off";
  return (
    <ListRow
      title={name}
      markers={
        <>
          {pending !== null && <Spinner className="size-3.5" />}
          {/* Off is the switch's to say; manual it cannot. */}
          {mode === "manual" && <ModeChip mode={mode} />}
        </>
      }
      sub={description}
      problem={error || problem}
      problemTone={error ? "bad" : undefined}
      dim={!on}
      onOpen={onOpen}
      action={<RowSwitch label={name} checked={on} disabled={!groupOn || pending !== null} onCheckedChange={change} />}
    />
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

/**
 * The Skills tab: every skill the agents can see from here, in fixed
 * sections, with an editor for the ones that are ours. Read again on open and
 * after every change.
 */
export function SkillsTab({ command, scope }: { command: PageCommand; scope: SkillsScope }) {
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
        setList({ ...l, skills, claudeBuiltins: l.claudeBuiltins ?? [] });
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

  const setClaudeBundled = async (on: boolean) => {
    const res = await command<{ claudeBundled: boolean }>("set_claude_bundled", { ...scopeArgs, on });
    setList((l) => (l ? { ...l, claudeBundled: res.claudeBundled } : l));
    refresh();
  };

  const setClaudeBuiltin = async (name: string, on: boolean) => {
    const res = await command<ClaudeBuiltin>("set_claude_builtin", { ...scopeArgs, name, on });
    setList((l) =>
      l ? { ...l, claudeBuiltins: l.claudeBuiltins.map((b) => (b.name === res.name ? res : b)) } : l,
    );
    refresh();
  };

  /** A Codex built-in is on or off; its files are Codex's, so manual is not offered. */
  const setCodexBuiltin = async (skill: Skill, on: boolean) => {
    const res = await command<Skill>("set_skill_mode", { ...scopeArgs, dir: skill.dir, mode: on ? "on" : "off" });
    upsert([res]);
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
  const claudeBuiltins = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (list?.claudeBuiltins ?? []).filter((b) => b.name.toLowerCase().includes(q));
  }, [list, query]);

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
  const { destinations, defaultDestination } = useMemo(() => destinationsOf(list), [list]);

  const renderSection = (kind: SectionKind) => {
    if (!list) return null;
    const skills = sections[kind];
    const count = kind === "claude" ? claudeBuiltins.length : skills.length;
    // A search shows only where it found something.
    if (searching && count === 0) return null;

    let title: string;
    let action: ReactNode;
    let note: string | undefined;
    let asideOf: ((skill: Skill) => string | undefined) | undefined;
    let rows: ReactNode = null;
    switch (kind) {
      case "yours":
        title = "Yours";
        if (skills.length === 0) note = "No skills yet. Add one to start.";
        break;
      case "private":
        if (skills.length === 0) return null;
        title = PROJECT_LABEL;
        break;
      case "project": {
        if (skills.length === 0) return null;
        // Titled by the full list, not the search's slice of it, so a search
        // does not rename the section under the reader.
        const repo = repoSectionTitle(list.skills.filter((s) => sectionOf(s) === "project"), destinations);
        title = repo.title ?? (projectName ? `Project: ${projectName}` : "Project");
        if (repo.perRow) {
          asideOf = (s) => folderLabel(s.folder, destinations) ?? (s.folder ? baseName(s.folder) : undefined);
        }
        break;
      }
      case "synced":
        title = "From claude.ai";
        action = <SectionSwitch label="Sync from claude.ai" on={list.claudeSync} onChange={setClaudeSync} />;
        if (!searching) {
          note = !list.claudeSync ? "Off. Claude won't load these." : skills.length === 0 ? "None synced yet." : undefined;
        }
        break;
      case "omniplex":
        if (skills.length === 0) return null;
        title = "Omniplex";
        if (!searching) note = "Ships with Omniplex and reaches every session. Read-only.";
        break;
      case "claude":
        title = "Claude Code built-in";
        action = <SectionSwitch label="Claude Code built-in skills" on={list.claudeBundled} onChange={setClaudeBundled} />;
        if (!searching && !list.claudeBundled) note = "Off. Claude won't load these.";
        rows = claudeBuiltins.map((b) => (
          <li key={b.name}>
            <BuiltinRow
              name={b.name}
              mode={b.mode}
              groupOn={list.claudeBundled}
              onChange={(on) => setClaudeBuiltin(b.name, on)}
            />
          </li>
        ));
        break;
      case "system":
        title = "Codex built-in";
        action = <SectionSwitch label="Codex built-in skills" on={list.codexBundled} onChange={setCodexBundled} />;
        if (!searching) {
          note = !list.codexBundled ? "Off. Codex won't load these." : skills.length === 0 ? "None found." : undefined;
        }
        rows = skills.map((skill) => (
          <li key={skill.dir}>
            <BuiltinRow
              name={skill.name}
              description={skill.description}
              mode={skill.mode}
              groupOn={list.codexBundled}
              problem={skill.problem}
              onOpen={() => openSkill(skill)}
              onChange={(on) => setCodexBuiltin(skill, on)}
            />
          </li>
        ));
        break;
      case "plugins":
        if (skills.length === 0) return null;
        title = "Plugins";
        asideOf = (s) => s.plugin;
        break;
    }

    return (
      <Section
        key={kind}
        title={title}
        count={count}
        open={sectionOpen(kind)}
        onOpenChange={(next) => setSectionOpen(kind, next)}
        action={action}
        note={note}
      >
        {count > 0 && (
          <ul>
            {rows ??
              skills.map((skill) => (
                <li key={skill.dir}>
                  <SkillRow skill={skill} aside={asideOf?.(skill)} onOpen={(s) => openSkill(s)} />
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
    <div className="relative flex h-full min-h-0 flex-col">
      <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
        <ListToolbar
          query={query}
          onQuery={changeQuery}
          searchLabel="Search skills"
          onAdd={() => {
            setAddSeq((n) => n + 1);
            setAdding(true);
          }}
        />

        <CommitStrip command={command} scopeArgs={scopeArgs} version={loads} />

        <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
          {error && <LoadError message={`Could not list skills. ${error}`} onRetry={refresh} busy={loading} />}
          {loading && !list && <Loading>Finding skills…</Loading>}
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
            destinations={destinations}
            startEditing={open.edit}
            onBack={() => setOpen(null)}
            onChanged={upsert}
            onRemoved={removed}
          />
        </div>
      )}

      <AddSheet
        key={addSeq}
        open={adding}
        onOpenChange={setAdding}
        command={command}
        scopeArgs={scopeArgs}
        destinations={destinations}
        defaultDestination={defaultDestination}
        onInstalled={upsert}
        onCreated={(skill) => {
          upsert([skill]);
          openSkill(skill, true);
        }}
      />
    </div>
  );
}
