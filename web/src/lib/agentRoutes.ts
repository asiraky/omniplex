import { AGENT_TABS, type AgentFocus, type AgentTab } from "./agentNav";

// The agent pages' URLs. They are main-column pages beside the sidebar, with
// paths of their own so a reload, a shared link and Back all land on them:
//
//   /agents/<id>/<tab>[?thread=<id>&epoch=3|?rev=4]   an agent's page, on one tab
//   /agents/new[?project=<id>]            the New agent form

export type AgentPage =
  | { kind: "agent"; agentId: string; tab: AgentTab; focus?: AgentFocus }
  | { kind: "new"; projectId?: string };

const PREFIX = "/agents/";

/** The page a URL names, or null for any path that is not an agent page. */
export function parseAgentPage(pathname: string, search = ""): AgentPage | null {
  if (!pathname.startsWith(PREFIX)) return null;
  const parts = pathname.slice(PREFIX.length).split("/").filter(Boolean).map(decodeSegment);
  const q = new URLSearchParams(search);
  if (parts[0] === "new" && parts.length === 1) {
    const projectId = q.get("project") || undefined;
    return { kind: "new", ...(projectId ? { projectId } : {}) };
  }
  if (parts.length < 1 || parts.length > 2) return null;
  // A tab this build does not know (an old link) still opens the agent.
  const tab = (AGENT_TABS as readonly string[]).includes(parts[1]) ? (parts[1] as AgentTab) : "prompt";
  const focus: AgentFocus = {};
  const thread = q.get("thread");
  if (thread) focus.thread = thread;
  const epoch = whole(q.get("epoch"));
  const rev = whole(q.get("rev"));
  if (epoch !== undefined) focus.epoch = epoch;
  if (rev !== undefined) focus.rev = rev;
  return { kind: "agent", agentId: parts[0], tab, ...(Object.keys(focus).length > 0 ? { focus } : {}) };
}

/** The URL for a page; parseAgentPage reads it back to the same page. */
export function agentPagePath(page: AgentPage): string {
  if (page.kind === "new") {
    return `${PREFIX}new${page.projectId ? `?project=${encodeURIComponent(page.projectId)}` : ""}`;
  }
  const q = new URLSearchParams();
  if (page.focus?.thread) q.set("thread", page.focus.thread);
  if (page.focus?.epoch !== undefined) q.set("epoch", String(page.focus.epoch));
  if (page.focus?.rev !== undefined) q.set("rev", String(page.focus.rev));
  const query = q.toString();
  return `${PREFIX}${encodeURIComponent(page.agentId)}/${page.tab}${query ? `?${query}` : ""}`;
}

// A malformed escape (a mangled link) is kept as typed rather than thrown
// during the first render: it names no agent, so the page says so.
function decodeSegment(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

function whole(v: string | null): number | undefined {
  if (v === null || !/^\d+$/.test(v)) return undefined;
  return Number(v);
}
