import { useMemo, useState } from "react";

import {
  HARNESSES,
  harnessLabel,
  matchesQuery,
  PROMPT_BUDGETS,
  promptReport,
  type Skill,
  type SkillHarness,
} from "~/lib/skills";

import { Marker, Segmented } from "./parts";

const count = (n: number) => n.toLocaleString();

/**
 * What one harness carries in its system prompt on every turn: the skill
 * descriptions that land there, longest first, so the ones worth trimming or
 * making manual are at the top.
 */
export function InPromptView({
  skills,
  query,
  onOpen,
}: {
  skills: Skill[];
  query: string;
  onOpen: (skill: Skill) => void;
}) {
  const [harness, setHarness] = useState<SkillHarness>("claude");
  const report = useMemo(() => promptReport(skills, harness), [skills, harness]);
  const shown = useMemo(() => report.entries.filter((e) => matchesQuery(e.skill, query)), [report, query]);
  const label = harnessLabel(harness);
  const kept = [
    report.manual > 0 ? `${count(report.manual)} manual` : "",
    report.off > 0 ? `${count(report.off)} off` : "",
  ].filter(Boolean);

  return (
    <div className="px-2 pt-2 pb-4">
      <Segmented label="Harness" value={harness} onChange={setHarness} options={HARNESSES} />

      <div className="mt-3 px-1">
        <p className="text-[13px]">
          <span className="font-semibold tabular-nums">{count(report.total)}</span> characters of descriptions from{" "}
          <span className="tabular-nums">{count(report.entries.length)}</span>{" "}
          {report.entries.length === 1 ? "skill" : "skills"} in every {label} prompt.
        </p>
        {report.over > 0 && (
          <p className="text-attention-foreground mt-1 text-[12.5px]">
            About {count(report.over)} characters over the estimated budget of {count(report.budget ?? 0)}.
          </p>
        )}
        <p className="text-muted-foreground mt-1 text-[12px] leading-snug">{PROMPT_BUDGETS[harness].note}</p>
        {kept.length > 0 && (
          <p className="text-muted-foreground mt-1 text-[12px] leading-snug">
            Kept out of the prompt: {kept.join(", ")}.
          </p>
        )}
      </div>

      {report.entries.length === 0 ? (
        <p className="text-muted-foreground px-1 py-8 text-center text-[12.5px]">
          No skill descriptions reach {label}.
        </p>
      ) : shown.length === 0 ? (
        <p className="text-muted-foreground px-1 py-8 text-center text-[12.5px]">
          No skills in the prompt match “{query.trim()}”.
        </p>
      ) : (
        <ol aria-label={`Descriptions in the ${label} prompt`} className="mt-3">
          {shown.map((entry) => (
            <li key={entry.skill.dir}>
              <button
                type="button"
                onClick={() => onOpen(entry.skill)}
                className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full flex-col gap-0.5 rounded-md px-2 py-2 text-left transition-colors outline-none focus-visible:ring-2"
              >
                <span className="flex w-full min-w-0 items-center gap-1.5">
                  <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{entry.skill.name}</span>
                  {entry.mode === "name-only" && <Marker>name only</Marker>}
                  {entry.cut > 0 && <Marker tone="attention">cut by {count(entry.cut)}</Marker>}
                  <span className="text-muted-foreground shrink-0 text-[12px] tabular-nums">{count(entry.chars)}</span>
                </span>
                {entry.mode === "auto" && entry.skill.description && (
                  <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug">
                    {entry.skill.description}
                  </span>
                )}
              </button>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
