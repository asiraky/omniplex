import type { AgentApi } from "~/lib/agentApi";
import type { AgentFocus } from "~/lib/agentNav";
import type { Agent } from "~/protocol";
import { RevisionsEditor } from "./RevisionsEditor";

/** The agent page's Prompt tab: its system prompt, versioned. Saving restarts the live thread. */
export function PromptTab({ agent, api, focus }: { agent: Agent; api: AgentApi; focus?: AgentFocus }) {
  return <RevisionsEditor key={agent.id} agent={agent} api={api} kind="prompt" focus={focus} />;
}
