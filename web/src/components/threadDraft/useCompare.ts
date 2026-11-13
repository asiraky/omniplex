import { useState } from "react";

import type { ModelSelection } from "~/components/ModelPicker";
import type { CompareRun, NewThreadInput } from "~/components/ThreadDraft";
import { addCompare, compareBranches, compareTitle, sameModel } from "./compare";
import { compareAgent, type AgentSources } from "./useAgentChoice";

const NONE: ModelSelection[] = [];

/**
 * The models added beside the draft's own, to send the prompt to each in its
 * own thread. Held for this draft only: a new draft starts with none.
 */
export function useCompare(sources: AgentSources, primary: ModelSelection, enabled: boolean) {
  const [picked, setPicked] = useState<ModelSelection[]>(NONE);
  // Off outside a single git folder, where every model would edit the same
  // files; the set waits for the scope to come back. The draft's own model
  // switched onto one already added is the same thread, so it drops out.
  const models = enabled ? picked.filter((m) => !sameModel(m, primary)) : NONE;
  const extras = models.map((pick) => ({ pick, agent: compareAgent(sources, pick) }));

  return {
    extras,
    comparing: extras.length > 0,
    add: (next: ModelSelection) =>
      setPicked((set) =>
        addCompare(
          set.filter((m) => !sameModel(m, primary)),
          primary,
          next,
        ),
      ),
    remove: (gone: ModelSelection) => setPicked((set) => set.filter((m) => !sameModel(m, gone))),
    clear: () => setPicked(NONE),
    /**
     * One run per model, the draft's own first. Every one gets a new worktree
     * off the same base, a branch of its own, and a title naming its model.
     */
    runs: (own: NewThreadInput, ownLabel: string): CompareRun[] => {
      const all = [
        { input: own, label: ownLabel },
        ...extras.map(({ agent: { label, ...agent } }) => ({
          input: { ...own, ...agent, agentSettingsExplicit: true },
          label,
        })),
      ];
      const branches = compareBranches(
        own.branch,
        all.map((r) => r.input.model),
      );
      return all.map(({ input, label }, i) => ({
        label,
        input: {
          ...input,
          workspace: "managed",
          workspacePath: "",
          branch: branches[i],
          title: compareTitle(input.text, label),
        },
      }));
    },
  };
}

export type CompareState = ReturnType<typeof useCompare>;
