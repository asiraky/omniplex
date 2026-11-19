import type { Agent } from "~/protocol";

// The compact-at threshold rules, apart from agentText so the composer's
// context meter can read them without pulling the agent page's diff helpers
// into the first load.

/** Claude Code will not compact earlier than this, whatever it is told. */
export const CLAUDE_MIN_COMPACT_AT = 100_000;

/** Whether a harness takes a compact-at threshold. Pi compacts on its own terms and ignores one. */
export function takesCompactAt(harness: string): boolean {
  return harness !== "pi";
}

/**
 * Where an agent's thread compacts, as its harness will take it; undefined for
 * the harness default, and for a harness that ignores the setting.
 */
export function agentCompactAt(agent: Agent | undefined): number | undefined {
  if (!agent?.compactAt || agent.compactAt <= 0 || !takesCompactAt(agent.harness)) return undefined;
  return agent.harness === "claude" ? Math.max(agent.compactAt, CLAUDE_MIN_COMPACT_AT) : agent.compactAt;
}
