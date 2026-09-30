import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import {
  harnessLabel,
  invocationSummary,
  joinWords,
  originLabel,
  skillOrigin,
  type Setup,
  type Skill,
  type SkillGroup,
  type SkillsList,
} from "~/lib/skills";
import { cn } from "~/lib/utils";

export type SkillsCommand = <T = unknown>(name: string, args: Record<string, unknown>) => Promise<T>;

/** What a slot is handed: enough to call the server and fold the answer back into the list. */
export interface SkillsContext {
  command: SkillsCommand;
  /** `threadId` / `projectId`, to spread into every command's args. */
  scopeArgs: Record<string, unknown>;
  list: SkillsList | null;
  setup?: Setup;
  /** True when there is a project to install into. */
  projectAvailable: boolean;
  /** Ask the server for the list again. */
  refresh: () => void;
  /** Put skills the server just returned into the list, replacing by dir. */
  upsert: (skills: Skill[]) => void;
}

/**
 * Where the install, commit and update flows attach. Each is rendered only
 * when given, so the surface works without any of them.
 */
export interface SkillsSlots {
  /** Toolbar, left of "New skill": the Install action. */
  install?: (ctx: SkillsContext) => ReactNode;
  /** Between the toolbar and the list: the commit bar. */
  commitBar?: (ctx: SkillsContext) => ReactNode;
  /** Right end of a source repo group's header. Not rendered for other groups. */
  groupAction?: (group: SkillGroup, ctx: SkillsContext) => ReactNode;
  /** Detail view actions, between Edit and Remove. Only asked for a skill with a source. */
  update?: (skill: Skill, ctx: SkillsContext) => ReactNode;
}

/** A small pill for the exceptions a row carries; rows with nothing to say have none. */
export function Marker({ tone = "quiet", children }: { tone?: "quiet" | "attention"; children: ReactNode }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-1.5 py-px text-[10.5px] leading-tight font-medium whitespace-nowrap",
        tone === "attention"
          ? "border-attention/40 bg-attention-surface text-attention-foreground"
          : "text-muted-foreground",
      )}
    >
      {children}
    </span>
  );
}

/**
 * The exceptions only: most skills are in every prompt and seen everywhere,
 * and say nothing. "Not in" is kept to skills that could be linked; a plugin
 * skill being Claude-only is what a plugin is, not news.
 */
export function SkillMarkers({ skill }: { skill: Skill }) {
  const { state, missing } = invocationSummary(skill);
  return (
    <>
      {state === "manual" && <Marker>manual</Marker>}
      {state === "off" && <Marker>off</Marker>}
      {state === "mixed" && <Marker tone="attention">harnesses differ</Marker>}
      {state === "unseen" && <Marker tone="attention">no harness reads it</Marker>}
      {state !== "unseen" && skill.editable && missing.length > 0 && (
        <Marker>not in {joinWords(missing.map(harnessLabel))}</Marker>
      )}
    </>
  );
}

export function OriginBadge({ skill }: { skill: Skill }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-1.5 py-px text-[10.5px] font-medium whitespace-nowrap",
        skillOrigin(skill).kind === "project"
          ? "border-primary/40 bg-primary/10 text-foreground"
          : "text-muted-foreground",
      )}
    >
      {originLabel(skill)}
    </span>
  );
}

/** A skill's problem as text on the page: a phone has no hover to hide it behind. */
export function ProblemText({ problem, className }: { problem?: string; className?: string }) {
  if (!problem) return null;
  return (
    <span className={cn("text-attention-foreground flex items-start gap-1.5 text-[12px] leading-snug", className)}>
      <TriangleAlertIcon className="mt-px size-3.5 shrink-0" aria-hidden />
      <span className="min-w-0 break-words">{problem}</span>
    </span>
  );
}

export function ErrorLine({ message, className }: { message: string; className?: string }) {
  return (
    <div role="alert" className={cn("text-destructive flex items-start gap-2 text-[12px]", className)}>
      <TriangleAlertIcon className="mt-px size-4 shrink-0" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  );
}

export function SectionHeading({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <h3 className={cn("text-muted-foreground mb-1.5 text-[11px] font-semibold tracking-wide uppercase", className)}>
      {children}
    </h3>
  );
}

/** A two-or-more-way switch in the app's pill style (see Usage). */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: {
  label: string;
  value: T;
  options: { id: T; label: string; count?: number }[];
  onChange: (value: T) => void;
  className?: string;
}) {
  return (
    <div role="tablist" aria-label={label} className={cn("bg-secondary/60 flex rounded-full p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.id}
          type="button"
          role="tab"
          aria-selected={value === o.id}
          onClick={() => onChange(o.id)}
          className={cn(
            // Thumb-sized on a phone; a pointer gets the compact pill.
            "focus-visible:ring-ring min-h-11 min-w-0 flex-1 truncate rounded-full px-3 py-1 text-[12.5px] font-medium whitespace-nowrap outline-none focus-visible:ring-2 md:min-h-0 md:px-2.5 md:text-[12px]",
            value === o.id ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
          {o.count !== undefined && <span className="text-muted-foreground ml-1 tabular-nums">{o.count}</span>}
        </button>
      ))}
    </div>
  );
}
