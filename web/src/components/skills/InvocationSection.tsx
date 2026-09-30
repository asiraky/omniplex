import { useId, useState } from "react";

import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import {
  errorText,
  HARNESSES,
  harnessLabel,
  harnessState,
  invocationSummary,
  joinWords,
  modeText,
  type Skill,
  type SkillHarness,
} from "~/lib/skills";

import { ErrorLine, SectionHeading, type SkillsCommand } from "./parts";

/**
 * Who can see the skill and what each harness does with it, with the two
 * things that can be changed from here: whether it is manual only, and
 * linking it for a harness that cannot reach it.
 */
export function InvocationSection({
  skill,
  command,
  scopeArgs,
  onChanged,
}: {
  skill: Skill;
  command: SkillsCommand;
  scopeArgs: Record<string, unknown>;
  /** The skill as the server returned it after a change. */
  onChanged: (skill: Skill) => void;
}) {
  const switchId = useId();
  // What is in flight: "manual", or a harness being linked.
  const [busy, setBusy] = useState<"" | "manual" | SkillHarness>("");
  const [error, setError] = useState("");
  const summary = invocationSummary(skill);

  const run = async (what: "manual" | SkillHarness, name: string, args: Record<string, unknown>) => {
    if (busy) return;
    setBusy(what);
    setError("");
    try {
      onChanged(await command<Skill>(name, { ...scopeArgs, dir: skill.dir, ...args }));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy("");
    }
  };
  const setManual = (manual: boolean) => void run("manual", "set_skill_invocation", { manual });

  const overridden = joinWords(summary.overridden.map(harnessLabel));

  return (
    <section aria-label="Invocation">
      <SectionHeading>Invocation</SectionHeading>
      <ul className="divide-y rounded-lg border">
        {HARNESSES.map((h) => {
          const state = harnessState(skill, h.id);
          return (
            <li key={h.id} className="flex items-center gap-3 px-3 py-2">
              <span className="w-12 shrink-0 text-[13px] font-medium">{h.label}</span>
              <span className="min-w-0 flex-1 text-[12.5px] leading-snug">
                <span className={state ? undefined : "text-muted-foreground"}>{modeText(state?.mode ?? null)}</span>
                {state?.by === "settings" && (
                  <span className="text-muted-foreground block">Set in {h.label}'s own settings.</span>
                )}
              </span>
              {!state && skill.editable && (
                <Button
                  variant="outline"
                  size="sm"
                  className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
                  disabled={busy !== ""}
                  onClick={() => void run(h.id, "link_skill", { harness: h.id })}
                >
                  {busy === h.id && <Spinner className="size-3.5" />}
                  Link for {h.label}
                </Button>
              )}
            </li>
          );
        })}
      </ul>

      {summary.state === "mixed" && (
        <div className="bg-attention-surface text-attention-foreground mt-2 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border px-3 py-2 text-[12.5px]">
          <span className="min-w-0 flex-1 basis-48 leading-snug">
            {summary.fixable
              ? "The harnesses disagree: the skill's files mark it manual for some and not others."
              : "The harnesses disagree because of a harness setting, which is changed in that harness."}
          </span>
          {summary.fixable && (
            <Button
              variant="outline"
              size="sm"
              className="text-foreground h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
              disabled={busy !== ""}
              onClick={() => setManual(true)}
            >
              {busy === "manual" && <Spinner className="size-3.5" />}
              Make it manual everywhere
            </Button>
          )}
        </div>
      )}

      {skill.editable && (
        <div className="mt-2">
          <label htmlFor={switchId} className="flex min-h-11 cursor-pointer items-center gap-3 px-1">
            <span className="min-w-0 flex-1">
              <span className="block text-[13px] font-medium">Manual only</span>
              <span className="text-muted-foreground block text-[12px] leading-snug">
                Keeps the description out of every prompt. You run the skill by name.
              </span>
            </span>
            {busy === "manual" && <Spinner className="size-3.5" />}
            <Switch
              id={switchId}
              checked={summary.manual}
              disabled={busy !== ""}
              onCheckedChange={setManual}
            />
          </label>
          {overridden && (
            <p className="text-muted-foreground px-1 text-[12px] leading-snug">
              {overridden} {summary.overridden.length === 1 ? "has" : "have"} a setting for this skill that wins over
              its files, so the switch does not change {summary.overridden.length === 1 ? "it" : "them"}.
            </p>
          )}
        </div>
      )}

      {error && <ErrorLine message={error} className="mt-2 px-1" />}
    </section>
  );
}
