import { useState } from "react";

import { ErrorLine, Segmented } from "~/components/tools/parts";
import type { AgentApi } from "~/lib/agentApi";
import type { AgentFocus } from "~/lib/agentNav";
import { fmtTokens } from "~/lib/format";
import { toast } from "~/lib/toast";
import { errorText } from "~/lib/utils";
import type { Agent } from "~/protocol";
import { CLAUDE_MIN_COMPACT_AT, COMPACT_PRESETS, compactRole, harnessName, takesCompactAt } from "./agentText";
import { InfoBox } from "./parts";
import { RevisionsEditor } from "./RevisionsEditor";

/**
 * The agent page's Compaction tab: the instructions its harness hands the
 * compactor, versioned like the prompt, and the token count it compacts at.
 */
export function CompactionTab({ agent, api, focus }: { agent: Agent; api: AgentApi; focus?: AgentFocus }) {
  return (
    <RevisionsEditor key={agent.id} agent={agent} api={api} kind="compact" focus={focus}>
      <CompactAt key={agent.id} agent={agent} api={api} />
      <div className="mt-4">
        <InfoBox>
          <HarnessNote harness={agent.harness} />
        </InfoBox>
      </div>
    </RevisionsEditor>
  );
}

/**
 * How each harness treats these instructions; they differ enough that one
 * sentence would mislead. It opens on the sentence the New agent form uses.
 */
function HarnessNote({ harness }: { harness: string }) {
  const role = compactRole(harness);
  switch (harness) {
    case "claude":
      return (
        <>
          {role} It keeps its <code className="font-mono">&lt;analysis&gt;</code> and{" "}
          <code className="font-mono">&lt;summary&gt;</code> structure. The Log tab shows the exact text sent each time.
        </>
      );
    case "codex":
      return <>{role} Codex reports no summary, so the Log tab shows token counts and the instructions only.</>;
    case "pi":
      return <>{role} Nor does it support compact-at: it compacts at its own threshold.</>;
    default:
      return <>{role}</>;
  }
}

type Choice = "default" | `${number}`;

/**
 * Compact-at as one tap: the harness default or one of three thresholds, never
 * a number field. It is read when a thread's process starts, so a change waits
 * for the next start or restart, and the toast says so.
 */
function CompactAt({ agent, api }: { agent: Agent; api: AgentApi }) {
  const supported = takesCompactAt(agent.harness);
  const saved = agent.compactAt ?? 0;
  const [picked, setPicked] = useState<Choice | null>(null);
  const choice: Choice = picked ?? (saved ? `${saved}` : "default");
  // A pick stands until the saved value catches up with it, so the control
  // does not flick back while the agents frame is on its way.
  const [pickedFor, setPickedFor] = useState(saved);
  if (pickedFor !== saved) {
    setPickedFor(saved);
    setPicked(null);
  }
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function store(tokens: number) {
    setSaving(true);
    setError(null);
    try {
      await api.save(agent.id, { compactAt: tokens });
      toast.success(
        `${tokens ? `Compacts at ${fmtTokens(tokens)}` : "Compacts at the harness default"}. Applies at the next start or restart.`,
      );
    } catch (e) {
      setError(errorText(e));
      setPicked(null);
    } finally {
      setSaving(false);
    }
  }

  function pick(c: Choice) {
    setError(null);
    setPicked(c);
    void store(c === "default" ? 0 : Number(c));
  }

  const options: { id: Choice; label: string }[] = [
    { id: "default", label: "Default" },
    ...COMPACT_PRESETS.map((n) => ({ id: `${n}` as Choice, label: fmtTokens(n) })),
  ];
  const floor = agent.harness === "claude" ? ` The minimum is ${fmtTokens(CLAUDE_MIN_COMPACT_AT)}.` : "";
  const codexCap = agent.harness === "codex" ? " Codex caps it at 90% of the model's context window." : "";

  return (
    <section aria-label="Compact at" className="mt-7">
      <h3 className="mb-2 text-[13.5px] font-semibold">Compact at</h3>
      {!supported ? (
        <p className="text-muted-foreground text-[12.5px] leading-snug">
          {harnessName(agent.harness)} does not support a compact-at threshold.
        </p>
      ) : (
        <>
          <Segmented
            label="Compact at"
            radio
            value={choice}
            options={options}
            onChange={pick}
            disabled={saving}
            className="w-full md:w-fit"
          />
          <p className="text-muted-foreground mt-2 text-[12.5px] leading-snug">
            {choice === "default"
              ? `Default follows ${harnessName(agent.harness)}.`
              : `Compacts when context reaches ${fmtTokens(Number(choice))} tokens.`}
            {floor}
            {codexCap} Lower means more compactions and less to lose in each one.
          </p>
          {error && <ErrorLine message={error} className="mt-2" />}
        </>
      )}
    </section>
  );
}
