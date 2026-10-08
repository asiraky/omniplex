import { useId } from "react";

import { Marker, ProblemText } from "~/components/tools/parts";
import { MODE_LABEL, NOT_COMMITTED, type Destination, type SkillMode } from "~/lib/skills";
import { cn } from "~/lib/utils";

/** A row's chip, only for a skill that is not on. */
export function ModeChip({ mode }: { mode: SkillMode }) {
  if (mode === "on") return null;
  return <Marker>{MODE_LABEL[mode]}</Marker>;
}

const DESTINATION_HINT: Record<Destination["kind"], string> = {
  project: "Every folder and worktree of this project. Never committed.",
  repo: "Shared with everyone who clones it.",
  personal: "Every project.",
};

/**
 * Where the skill goes. A list rather than a menu: the line under each choice
 * is what tells them apart. With one place to go there is nothing to pick,
 * but a main checkout's warning still shows.
 */
export function DestinationPicker({
  label,
  destinations,
  value,
  onChange,
  disabled,
}: {
  label: string;
  destinations: Destination[];
  value: string;
  onChange: (folder: string) => void;
  disabled?: boolean;
}) {
  const labelId = useId();
  const chosen = destinations.find((d) => d.folder === value);
  if (destinations.length < 2 && !chosen?.main) return null;
  return (
    <div className="space-y-1.5">
      {destinations.length > 1 && (
        <>
          <p id={labelId} className="text-sm leading-none font-medium">
            {label}
          </p>
          <div role="radiogroup" aria-labelledby={labelId} className="flex flex-col gap-1.5">
            {destinations.map((d) => {
              const picked = d.folder === value;
              return (
                <button
                  key={`${d.kind}:${d.folder}`}
                  type="button"
                  role="radio"
                  aria-checked={picked}
                  disabled={disabled}
                  onClick={() => onChange(d.folder)}
                  className={cn(
                    "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-1.5 text-left transition-colors outline-none focus-visible:ring-2 disabled:cursor-default",
                    picked ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
                  )}
                >
                  <span className="text-[13px] leading-tight">{d.label}</span>{" "}
                  <span className="text-muted-foreground text-[11.5px] leading-tight">{DESTINATION_HINT[d.kind]}</span>
                </button>
              );
            })}
          </div>
        </>
      )}
      {chosen?.main && <ProblemText problem={NOT_COMMITTED} />}
    </div>
  );
}
