import type { ReactNode } from "react";

import { CommandEmpty, CommandGroup } from "~/components/ui/command";
import type { Project, ThreadMeta } from "~/protocol";

import type { PaletteAction } from "../CommandPalette";
import { PaletteRow } from "./PaletteRow";
import { matchScore, searchThreads } from "./search";

/** Recent threads shown before anything is typed. */
const RECENT = 8;
/** Threads a query can turn up: enough to search the whole list on a phone. */
const FOUND = 50;

const NEW_THREAD = { label: "New thread in…", keywords: ["create", "start", "draft"] };
const RENAME = { label: "Rename thread…", keywords: ["title", "name"] };

interface Section {
  key: string;
  heading?: string;
  score: number;
  rows: ReactNode[];
}

/**
 * The palette's first page. Empty, it is a menu: start a thread, the recent
 * threads, then what can be done to the open thread and to the app. Typed
 * into, it is one search over all of those, and whichever section matches
 * best comes first — a thread's title can be the better answer to a query
 * than any action, or the other way round.
 */
export function RootPage({
  query,
  threads,
  activeId,
  projects,
  actions,
  onSelectThread,
  onNewThreadIn,
  onRename,
  onAction,
}: {
  query: string;
  threads: ThreadMeta[];
  activeId: string | null;
  projects: Project[];
  actions: PaletteAction[];
  onSelectThread: (id: string) => void;
  onNewThreadIn: () => void;
  /** Absent when there is no thread to rename. */
  onRename?: () => void;
  onAction: (action: PaletteAction) => void;
}) {
  const searching = query.trim().length > 0;
  const projectName = new Map(projects.map((p) => [p.id, p.name]));
  const sections: Section[] = [];

  const newScore = matchScore(query, NEW_THREAD.label, NEW_THREAD.keywords);
  if (newScore > 0) {
    sections.push({
      key: "new",
      score: newScore,
      rows: [
        <PaletteRow
          key="new"
          value="new-thread"
          label={NEW_THREAD.label}
          command="newThreadIn"
          opensStep
          onSelect={onNewThreadIn}
        />,
      ],
    });
  }

  // The open thread is where the user already is, so it is never a result.
  const found = searchThreads(threads, query, searching ? FOUND : RECENT, activeId);
  if (found.length > 0) {
    sections.push({
      key: "threads",
      heading: searching ? "Threads" : "Recent",
      score: Math.max(...found.map((t) => matchScore(query, t.title || "Untitled"))),
      rows: found.map((t) => (
        <PaletteRow
          key={t.id}
          value={`thread:${t.id}`}
          label={t.title || "Untitled"}
          detail={t.projectId ? projectName.get(t.projectId) : undefined}
          onSelect={() => onSelectThread(t.id)}
        />
      )),
    });
  }

  for (const group of ["Thread", "App"] as const) {
    const rows: ReactNode[] = [];
    let best = 0;
    if (group === "Thread" && onRename) {
      const score = matchScore(query, RENAME.label, RENAME.keywords);
      if (score > 0) {
        best = score;
        rows.push(
          <PaletteRow key="rename" value="rename" label={RENAME.label} opensStep onSelect={onRename} />,
        );
      }
    }
    const scored = actions
      .filter((a) => a.group === group)
      .map((a) => ({ a, score: matchScore(query, a.label, a.keywords) }))
      .filter((s) => s.score > 0);
    // Unsearched, the app's own order stands; searched, the best match leads.
    if (searching) scored.sort((x, y) => y.score - x.score);
    for (const { a, score } of scored) {
      best = Math.max(best, score);
      rows.push(
        <PaletteRow
          key={a.id}
          value={`action:${a.id}`}
          label={a.label}
          command={a.command}
          onSelect={() => onAction(a)}
        />,
      );
    }
    if (rows.length > 0) sections.push({ key: group, heading: group, score: best, rows });
  }

  // Stable, so equal sections keep the order the empty palette shows them in.
  if (searching) sections.sort((a, b) => b.score - a.score);

  return (
    <>
      <CommandEmpty>Nothing matches that.</CommandEmpty>
      {sections.map((s) => (
        <CommandGroup key={s.key} heading={s.heading}>
          {s.rows}
        </CommandGroup>
      ))}
    </>
  );
}
