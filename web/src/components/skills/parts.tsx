import { TriangleAlertIcon } from "lucide-react";

import { HARNESSES, scopeLabel, type Skill, type SkillHarness } from "~/lib/skills";
import { cn } from "~/lib/utils";

export type SkillsCommand = <T = unknown>(name: string, args: Record<string, unknown>) => Promise<T>;

/**
 * All three harnesses, always in the same order and the same place, with the
 * ones that cannot see the skill faded out: down a list the columns line up,
 * so "which of these is Claude missing" is a scan rather than a read.
 */
export function HarnessChips({ harnesses, className }: { harnesses: SkillHarness[]; className?: string }) {
  const visible = HARNESSES.filter((h) => harnesses.includes(h.id));
  const label = visible.length ? `Visible to ${visible.map((h) => h.label).join(", ")}` : "No harness sees this";
  return (
    <span className={cn("flex shrink-0 items-center gap-0.5", className)} title={label}>
      <span className="sr-only">{label}</span>
      {HARNESSES.map((h) => {
        const on = harnesses.includes(h.id);
        return (
          <span
            key={h.id}
            aria-hidden
            className={cn(
              "rounded border px-1 py-px font-mono text-[9.5px] leading-tight",
              on ? "bg-secondary/60 text-foreground/80 border-border" : "text-muted-foreground/35 border-transparent line-through",
            )}
          >
            {h.label.toLowerCase()}
          </span>
        );
      })}
    </span>
  );
}

export function ScopeBadge({ skill }: { skill: Pick<Skill, "scope" | "plugin"> }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-1.5 py-px text-[10px] font-medium whitespace-nowrap",
        skill.scope === "project" ? "border-primary/40 bg-primary/10 text-foreground" : "text-muted-foreground",
      )}
    >
      {scopeLabel(skill)}
    </span>
  );
}

export function ProblemIcon({ problem, className }: { problem?: string; className?: string }) {
  if (!problem) return null;
  return (
    <span className={cn("text-attention-foreground shrink-0", className)} title={problem}>
      <TriangleAlertIcon className="size-3.5" aria-hidden />
      <span className="sr-only">Problem: {problem}</span>
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
            "focus-visible:ring-ring min-w-0 flex-1 truncate rounded-full px-2.5 py-1 text-[12px] font-medium whitespace-nowrap outline-none focus-visible:ring-2",
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
