import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { MODE_LABEL, type SkillMode } from "~/lib/skills";
import { cn } from "~/lib/utils";

export type SkillsCommand = <T = unknown>(name: string, args: Record<string, unknown>) => Promise<T>;

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

/** A row's chip, only for a skill that is not on. */
export function ModeChip({ mode }: { mode: SkillMode }) {
  if (mode === "on") return null;
  return <Marker>{MODE_LABEL[mode]}</Marker>;
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

/**
 * A two-or-more-way switch in the app's pill style (see Usage). Tabs by
 * default; `radio` for a setting, where the choice is a value, not a view.
 */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  radio = false,
  disabled = false,
  className,
}: {
  label: string;
  value: T;
  options: { id: T; label: string; count?: number }[];
  onChange: (value: T) => void;
  radio?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <div
      role={radio ? "radiogroup" : "tablist"}
      aria-label={label}
      className={cn("bg-secondary/60 flex rounded-full p-0.5", className)}
    >
      {options.map((o) => {
        const selected = value === o.id;
        return (
          <button
            key={o.id}
            type="button"
            role={radio ? "radio" : "tab"}
            aria-selected={radio ? undefined : selected}
            aria-checked={radio ? selected : undefined}
            disabled={disabled}
            onClick={() => onChange(o.id)}
            className={cn(
              // Thumb-sized on a phone; a pointer gets the compact pill. Each
              // tab starts from its own label's width: split evenly, the longer
              // label is cut short while the row still has room to spare.
              "focus-visible:ring-ring min-h-11 min-w-0 flex-auto truncate rounded-full px-3 py-1 text-[12.5px] font-medium whitespace-nowrap outline-none focus-visible:ring-2 disabled:cursor-default md:min-h-0 md:px-2.5 md:text-[12px]",
              selected ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
            {o.count !== undefined && <span className="text-muted-foreground ml-1 tabular-nums">{o.count}</span>}
          </button>
        );
      })}
    </div>
  );
}
