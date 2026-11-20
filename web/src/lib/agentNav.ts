import { createContext, useContext } from "react";
import type { Agent } from "../protocol";

/** The agent page's tabs, in order. */
export const AGENT_TABS = ["prompt", "compact", "log", "settings"] as const;
export type AgentTab = (typeof AGENT_TABS)[number];

/** Where to land on the agent page: a Log row to open, a revision to show. */
export interface AgentFocus {
  /** The thread whose log the epoch is in; absent is the live one. */
  thread?: string;
  epoch?: number;
  rev?: number;
}

/**
 * What the thread view needs to know about agents without the app's state:
 * the agent the attached thread runs for (absent on any other thread), and a
 * way to open an agent's page or one of its threads. App provides it; the default does nothing.
 */
export interface AgentNav {
  /** The agent the attached thread runs for: its live thread, or one it archived. */
  current?: Agent;
  /** The attached thread, which need not be current's live one. */
  threadId?: string;
  openAgent(agentId: string, tab?: AgentTab, focus?: AgentFocus): void;
  /** Attach a thread, scrolled to the item at seq when one is given. */
  openThread(threadId: string, seq?: number): void;
}

export const AgentNavContext = createContext<AgentNav>({ openAgent: () => {}, openThread: () => {} });

export function useAgentNav(): AgentNav {
  return useContext(AgentNavContext);
}
