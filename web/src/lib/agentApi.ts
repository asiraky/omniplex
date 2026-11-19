import type {
  Agent,
  AgentRevision,
  AgentTextKind,
  ArchivedAgentThread,
  CompactionDetail,
  CompactionRow,
} from "../protocol";

/** Sends one ws command and resolves with its result (Client.command). */
export type Command = (name: string, args: Record<string, unknown>) => Promise<any>;

/** What create_agent takes. `start` with a firstMessage starts its thread too. */
export interface NewAgent {
  projectId: string;
  name: string;
  harness: string;
  instance?: string;
  model?: string;
  effort?: string;
  mode?: string;
  folderId?: string;
  workspace: string;
  compactAt?: number;
  kickoff?: string;
  prompt: string;
  compact: string;
  /** Start the first thread now, with kickoff as its first message. */
  start: boolean;
}

/** The fields save_agent changes; absent ones stay as they are. */
export type AgentPatch = Partial<
  Pick<Agent, "name" | "model" | "effort" | "mode" | "folderId" | "workspace" | "compactAt" | "kickoff" | "rawLog">
>;

/** What saving a prompt revision did to the live thread. */
export type RestartOutcome = "none" | "now" | "pending";

/** The agent commands, typed. Every agent change also arrives as an `agents` frame. */
export function agentApi(command: Command) {
  return {
    create: (a: NewAgent): Promise<{ agent: Agent; threadId?: string }> => command("create_agent", { ...a }),
    save: (agentId: string, patch: AgentPatch): Promise<{ agent: Agent }> =>
      command("save_agent", { agentId, ...patch }),
    archive: (agentId: string): Promise<unknown> => command("archive_agent", { agentId }),
    /** Archive the live thread, if any, and start a fresh one with this first message. */
    newThread: (agentId: string, firstMessage: string): Promise<{ threadId: string }> =>
      command("agent_new_thread", { agentId, firstMessage }),
    revisions: async (agentId: string, kind: AgentTextKind): Promise<AgentRevision[]> =>
      ((await command("agent_revisions", { agentId, kind })) as { revisions: AgentRevision[] }).revisions,
    /** A new revision; reverting is saving an old body again. */
    saveRevision: (
      agentId: string,
      kind: AgentTextKind,
      body: string,
      reason: string,
    ): Promise<{ revision: AgentRevision; restart: RestartOutcome }> =>
      command("save_agent_revision", { agentId, kind, body, reason }),
    /** The fixed text Omniplex puts between its own instructions and the agent's prompt. */
    preamble: async (): Promise<string> => ((await command("agent_preamble", {})) as { text: string }).text,
    /** Metadata only. threadId picks an archived thread's log; absent is the live one. */
    log: (
      agentId: string,
      threadId?: string,
    ): Promise<{ threadId?: string; compactions: CompactionRow[]; archived: ArchivedAgentThread[] }> =>
      command("agent_log", { agentId, ...(threadId ? { threadId } : {}) }),
    compaction: async (agentId: string, id: number): Promise<CompactionDetail> =>
      ((await command("agent_compaction", { agentId, id })) as { compaction: CompactionDetail }).compaction,
  };
}

export type AgentApi = ReturnType<typeof agentApi>;
