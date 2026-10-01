// The logic behind the MCP and Sign-ins tabs of the Skills page, kept
// out of the components so the forms stay dumb. The one security-sensitive
// rule lives here: a stored header or env value is never echoed, so a form
// row for it starts blank, and blank on save means "keep what is stored".

import { deriveInstanceId } from "~/lib/providerSpec";
import type {
  Cli,
  CliAccount,
  CliAccountStatus,
  CliSpec,
  FoundServer,
  McpAccountDraft,
  McpDraft,
  McpHarness,
  McpKind,
  McpServer,
  McpServerAccount,
  McpServerStatus,
  ThreadMcp,
  ThreadMcpStatus,
} from "~/protocol";

/** Server names and CLI ids share the server's rule. */
const NAME_RULE = /^[a-z0-9][a-z0-9_-]{0,47}$/;
const RESERVED = "omniplex";

/** A remote server is reached at a URL; anything else is a command we start. */
export function serverKind(s: { url?: string }): McpKind {
  return s.url ? "http" : "stdio";
}

/** The agents that can run this kind of server, in the server's order. */
export function harnessesFor(harnesses: McpHarness[], kind: McpKind): McpHarness[] {
  return harnesses.filter((h) => h.transports.includes(kind));
}

/** Turn one agent on or off, leaving entries for agents not shown untouched. */
export function toggleOff(off: string[], id: string, on: boolean): string[] {
  const rest = off.filter((x) => x !== id);
  return on ? rest : [...rest, id];
}

/** The second line of a server row: where it lives. */
export function serverWhere(s: { url?: string; command?: string; args?: string[] }): string {
  if (s.url) {
    try {
      return new URL(s.url).host || s.url;
    } catch {
      return s.url;
    }
  }
  return [s.command ?? "", ...(s.args ?? [])].join(" ").trim();
}

/** "A", "A and B", "A, B and C". */
export function joinNames(names: string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

export function nameProblem(name: string, what: string): string | null {
  if (!name) return `Give the ${what} a name.`;
  if (name === RESERVED) return `${RESERVED} is taken. Pick another name.`;
  if (!NAME_RULE.test(name)) {
    return "Use lowercase letters, digits, - and _ (48 at most), starting with a letter or digit.";
  }
  return null;
}

// ---- name/value rows ----

export interface Row {
  /** Stable React key; rows are added and removed in place. */
  key: number;
  name: string;
  value: string;
  /** A value is stored for this name; blank keeps it. */
  stored: boolean;
}

let rowSeq = 0;

export function newRow(name = "", value = "", stored = false): Row {
  return { key: ++rowSeq, name, value, stored };
}

/**
 * Rows to a record. Fully blank rows are dropped. A stored name left blank
 * travels as "" (keep); a new name with no value is a mistake, not a delete.
 */
export function rowsToRecord(
  rows: Row[],
  opts: { keepBlankStored: boolean; allowBlank?: boolean },
): { record: Record<string, string> } | { error: string } {
  const record: Record<string, string> = {};
  for (const row of rows) {
    const name = row.name.trim();
    if (!name && !row.value) continue;
    if (!name) return { error: "Every value needs a name." };
    if (name in record) return { error: `${name} is listed twice.` };
    if (!row.value && !(opts.keepBlankStored && row.stored) && !opts.allowBlank) {
      return { error: `Give ${name} a value.` };
    }
    record[name] = row.value;
  }
  return { record };
}

// ---- the server form ----

export interface ServerForm {
  name: string;
  kind: McpKind;
  url: string;
  command: string;
  /** One argument per line, so an argument with spaces survives. */
  args: string;
  headers: Row[];
  env: Row[];
  /** Agent ids that do not get it. */
  off: string[];
}

export function emptyServerForm(): ServerForm {
  return { name: "", kind: "http", url: "", command: "", args: "", headers: [], env: [], off: [] };
}

/** A parsed paste becomes a form with every value filled in. */
export function formFromDraft(draft: McpDraft): ServerForm {
  return {
    name: draft.name ?? "",
    kind: serverKind(draft),
    url: draft.url ?? "",
    command: draft.command ?? "",
    args: (draft.args ?? []).join("\n"),
    headers: Object.entries(draft.headers ?? {}).map(([k, v]) => newRow(k, v)),
    env: Object.entries(draft.env ?? {}).map(([k, v]) => newRow(k, v)),
    off: [],
  };
}

/** A stored server becomes a form whose values are blank, meaning unchanged. */
export function formFromServer(s: McpServer): ServerForm {
  return {
    name: s.name,
    kind: serverKind(s),
    url: s.url ?? "",
    command: s.command ?? "",
    args: (s.args ?? []).join("\n"),
    headers: s.headerNames.map((n) => newRow(n, "", true)),
    env: s.envNames.map((n) => newRow(n, "", true)),
    off: [...s.off],
  };
}

export type SaveServerArgs = {
  server: McpDraft & { off: string[] };
  previousName?: string;
};

/** The save_mcp_server argument for a form, or what is wrong with it. */
export function serverSaveArgs(
  form: ServerForm,
  previousName?: string,
): { args: SaveServerArgs } | { error: string } {
  const name = form.name.trim();
  const problem = nameProblem(name, "server");
  if (problem) return { error: problem };

  let draft: McpDraft;
  if (form.kind === "http") {
    const url = form.url.trim();
    let parsed: URL | null = null;
    try {
      parsed = new URL(url);
    } catch {
      // handled below
    }
    if (!parsed || (parsed.protocol !== "https:" && parsed.protocol !== "http:")) {
      return { error: "Enter the server's address, starting with https://." };
    }
    const headers = rowsToRecord(form.headers, { keepBlankStored: true });
    if ("error" in headers) return headers;
    draft = { name, url, env: {}, headers: headers.record };
  } else {
    const command = form.command.trim();
    if (!command) return { error: "Enter the command that starts the server." };
    const env = rowsToRecord(form.env, { keepBlankStored: true });
    if ("error" in env) return env;
    const args = form.args
      .split("\n")
      .map((a) => a.trim())
      .filter(Boolean);
    draft = { name, command, args, env: env.record, headers: {} };
  }
  const args: SaveServerArgs = { server: { ...draft, off: form.off } };
  if (previousName) args.previousName = previousName;
  return { args };
}

/**
 * Sign in is on offer for a URL server Omniplex has not signed in to, and for
 * one whose stored sign-in no longer works (a refresh that failed).
 */
export function offersSignIn(s: Pick<McpServer, "url" | "oauth" | "status">): boolean {
  if (!s.url) return false;
  return s.status === "sign_in" || (!s.oauth && s.status !== "connected");
}

// ---- a server's further accounts ----

/** What agents get an account as. Mirrors mcp.AccountName. */
export function accountName(server: string, label: string): string {
  return `${server}-${label}`;
}

/**
 * Every server a session gets from Omniplex: each server under its own name,
 * then each of its accounts under theirs, with the server it belongs to.
 */
export function sessionServers(servers: McpServer[]): { name: string; url?: string; server: string }[] {
  return servers.flatMap((s) => [
    { name: s.name, url: s.url, server: s.name },
    ...(s.accounts ?? []).map((a) => ({ name: a.name, url: s.url, server: s.name })),
  ]);
}

/** One value an account may have of its own, for a name the server has. */
export interface AccountValue {
  name: string;
  value: string;
  /** The account has a stored value of its own; blank keeps it. */
  own: boolean;
}

export interface AccountForm {
  label: string;
  values: AccountValue[];
}

/** The names an account can override: the server's headers, or its env. */
function valueNames(s: McpServer): string[] {
  return serverKind(s) === "http" ? s.headerNames : s.envNames;
}

export function accountForm(s: McpServer, a?: McpServerAccount): AccountForm {
  const own = new Set(a ? (serverKind(s) === "http" ? a.headerNames : a.envNames) : []);
  return {
    label: a?.label ?? "",
    values: valueNames(s).map((name) => ({ name, value: "", own: own.has(name) })),
  };
}

/**
 * The save_mcp_account argument, or what is wrong with the form. A typed
 * value is the account's own; a blank one it already had is kept; any other
 * name is left out, so the account uses the server's value.
 */
export function accountSaveArgs(
  form: AccountForm,
  s: McpServer,
  previous?: McpServerAccount,
): { args: { server: string; account: McpAccountDraft; previousLabel?: string } } | { error: string } {
  const label = form.label.trim();
  if (!label) return { error: "Give the account a label." };
  if (!NAME_RULE.test(label)) {
    return { error: "Use lowercase letters, digits, - and _ in the label, starting with a letter or digit." };
  }
  const name = accountName(s.name, label);
  if (name.length > 48) {
    return { error: `${name} is too long. Server name and label together get 47 characters.` };
  }
  if (s.accounts.some((a) => a.label === label && a.label !== previous?.label)) {
    return { error: `${s.name} already has an account labelled ${label}.` };
  }
  const values: Record<string, string> = {};
  for (const v of form.values) if (v.value || v.own) values[v.name] = v.value;
  const http = serverKind(s) === "http";
  const account: McpAccountDraft = { label, env: http ? {} : values, headers: http ? values : {} };
  return { args: previous ? { server: s.name, account, previousLabel: previous.label } : { server: s.name, account } };
}

// ---- the sign-in (CLI) form ----

export interface CliForm {
  name: string;
  statusCommand: string;
  signedInPattern: string;
  signInCommand: string;
  prepareCommand: string;
  accountEnv: Row[];
}

export function cliForm(cli?: Cli): CliForm {
  return {
    name: cli?.name ?? "",
    statusCommand: cli?.statusCommand ?? "",
    signedInPattern: cli?.signedInPattern ?? "",
    signInCommand: cli?.signInCommand ?? "",
    prepareCommand: cli?.prepareCommand ?? "",
    accountEnv: Object.entries(cli?.accountEnv ?? {}).map(([k, v]) => newRow(k, v)),
  };
}

/**
 * The save_cli argument. A new sign-in's id comes from its name; an existing
 * one keeps its id and its accounts, which this form does not edit.
 */
export function cliSaveArgs(
  form: CliForm,
  existing: Cli | undefined,
  takenIds: string[],
): { args: { cli: CliSpec; previousId?: string } } | { error: string } {
  const name = form.name.trim();
  if (!name) return { error: "Give the sign-in a name." };
  if (!form.statusCommand.trim()) return { error: "Enter the command that checks the sign-in." };
  if (!form.signInCommand.trim()) return { error: "Enter the command that signs in." };
  const env = rowsToRecord(form.accountEnv, { keepBlankStored: false, allowBlank: true });
  if ("error" in env) return env;
  const id = existing?.id ?? deriveInstanceId(name, takenIds).slice(0, 48);
  const problem = nameProblem(id, "sign-in");
  if (problem) return { error: problem };
  const cli: CliSpec = {
    id,
    name,
    statusCommand: form.statusCommand.trim(),
    signedInPattern: form.signedInPattern.trim(),
    signInCommand: form.signInCommand.trim(),
    prepareCommand: form.prepareCommand.trim(),
    accountEnv: env.record,
    accounts: (existing?.accounts ?? []).map(({ name, env }) => ({ name, env })),
  };
  return { args: existing ? { cli, previousId: existing.id } : { cli } };
}

// ---- keeping the fetched list current without fetching it again ----

/** Replace the entry with key `previous ?? key(item)`, or append it. */
export function upsert<T>(list: T[], item: T, key: (t: T) => string, previous?: string): T[] {
  const target = previous ?? key(item);
  const i = list.findIndex((t) => key(t) === target);
  if (i < 0) return [...list, item];
  return list.map((t, j) => (j === i ? item : t));
}

// ---- what the lists show ----

/** A status word for a row, and how loud it is. */
export interface Mark {
  label: string;
  tone: "quiet" | "good" | "attention" | "bad";
}

/** A status says something only when it needs a hand. */
export function statusMark(status: McpServerStatus): Mark | null {
  if (status === "sign_in") return { label: "Sign in", tone: "attention" };
  if (status === "failed") return { label: "Failed", tone: "bad" };
  return null;
}

/** A server's row speaks for the worst of its accounts, the first included. */
export function serverMark(s: McpServer): Mark | null {
  const all = [s.status, ...(s.accounts ?? []).map((a) => a.status)];
  if (all.includes("failed")) return statusMark("failed");
  if (all.includes("sign_in")) return statusMark("sign_in");
  return null;
}

/**
 * Which agents get it, when that is not all of them: "Off" for none, else
 * "Only Claude and Codex". Nothing when every agent that can run it does.
 */
export function offSummary(s: McpServer, harnesses: McpHarness[]): string | null {
  const able = harnessesFor(harnesses, serverKind(s));
  const on = able.filter((h) => !s.off.includes(h.id));
  if (able.length === 0 || on.length === able.length) return null;
  if (on.length === 0) return "Off";
  return `Only ${joinNames(on.map((h) => h.name))}`;
}

const ACCOUNT_RANK: Record<CliAccountStatus, number> = { failed: 3, signed_out: 2, unchecked: 1, signed_in: 0 };

export function accountMark(status: CliAccountStatus): Mark | null {
  if (status === "failed") return { label: "Failed", tone: "bad" };
  if (status === "signed_out") return { label: "Signed out", tone: "attention" };
  if (status === "signed_in") return { label: "Signed in", tone: "good" };
  return null;
}

/** A sign-in's row speaks for its worst account. */
export function cliMark(accounts: Pick<CliAccount, "status">[]): Mark | null {
  if (accounts.length === 0) return { label: "No accounts", tone: "quiet" };
  const worst = accounts.reduce((w, a) => (ACCOUNT_RANK[a.status] > ACCOUNT_RANK[w] ? a.status : w), "signed_in" as CliAccountStatus);
  // All signed in is the normal case: the row says nothing.
  return worst === "signed_in" ? null : accountMark(worst);
}

/** Something in the list wants the reader: a tab's dot. */
export function serversNeedAttention(servers: McpServer[]): boolean {
  return servers.some((s) => serverMark(s) !== null);
}

export function clisNeedAttention(clis: Cli[]): boolean {
  return clis.some((c) => c.accounts.some((a) => a.status === "failed" || a.status === "signed_out"));
}

/** Matches a server or found server on its name or where it runs. */
export function serverMatches(s: { name: string; url?: string; command?: string; args?: string[] }, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return s.name.toLowerCase().includes(q) || serverWhere(s).toLowerCase().includes(q);
}

export function cliMatches(c: Cli, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    c.name.toLowerCase().includes(q) ||
    c.id.toLowerCase().includes(q) ||
    c.accounts.some((a) => a.name.toLowerCase().includes(q))
  );
}

/** Found servers per agent, in the server's agent order; agents with none are left out. */
export function foundByHarness(found: FoundServer[], harnesses: McpHarness[]): { harness: McpHarness; servers: FoundServer[] }[] {
  const known = new Map(harnesses.map((h) => [h.id, h]));
  const out = new Map<string, FoundServer[]>();
  for (const f of found) out.set(f.harness, [...(out.get(f.harness) ?? []), f]);
  const order = [...harnesses.map((h) => h.id), ...[...out.keys()].filter((id) => !known.has(id))];
  return order
    .filter((id) => out.has(id))
    .map((id) => ({ harness: known.get(id) ?? { id, name: id, transports: [] }, servers: out.get(id)! }));
}

/** The one value add_found_server takes to tell two same-name entries apart. */
export function foundWhere(f: FoundServer): string {
  return f.url ?? f.command ?? "";
}

// ---- the thread's live view ----

const LIVE_MARK: Record<ThreadMcpStatus, Mark> = {
  connected: { label: "Connected", tone: "good" },
  needs_auth: { label: "Sign in", tone: "attention" },
  failed: { label: "Failed", tone: "bad" },
  pending: { label: "Starting", tone: "quiet" },
  disabled: { label: "Off", tone: "quiet" },
};

export function liveMark(status: ThreadMcpStatus): Mark {
  return LIVE_MARK[status];
}

const LIVE_RANK: Record<ThreadMcpStatus, number> = { failed: 0, needs_auth: 1, pending: 2, disabled: 3, connected: 4 };

/** Problems first, then by name: the rows that need a hand are the ones read. */
export function sortLive(servers: ThreadMcp[]): ThreadMcp[] {
  return [...servers].sort(
    (a, b) => LIVE_RANK[a.status] - LIVE_RANK[b.status] || a.name.toLowerCase().localeCompare(b.name.toLowerCase()),
  );
}

/**
 * What a live row offers. Sign in for our remote server that wants it,
 * reconnect for our server that failed, and for a server from the agent's
 * own config only the word that it is not ours to fix.
 */
export function liveAction(
  s: ThreadMcp,
  ours: Pick<McpServer, "name" | "url">[],
): "sign_in" | "reconnect" | "theirs" | null {
  if (!canReconnect(s.status) || s.name === BUILT_IN_MCP) return null;
  const own = ours.some((o) => o.name === s.name);
  if (!own) return "theirs";
  if (s.status === "needs_auth" && ownsSignIn(s.name, ours)) return "sign_in";
  return "reconnect";
}

/**
 * The server Omniplex gives every thread for its own tools. The name is
 * reserved (mcp.ReservedName), so no user server can take it.
 */
export const BUILT_IN_MCP = "omniplex";

/** Where a server in a thread's report came from. */
export function liveSource(name: string, ours: Pick<McpServer, "name">[]): "ours" | "built_in" | "theirs" {
  if (name === BUILT_IN_MCP) return "built_in";
  return ours.some((o) => o.name === name) ? "ours" : "theirs";
}

/** The session can be asked to try a server again. */
export function canReconnect(status: ThreadMcpStatus): boolean {
  return status === "needs_auth" || status === "failed";
}

/** Omniplex holds the sign-in for this server: it is ours, and remote. */
export function ownsSignIn(name: string, servers: Pick<McpServer, "name" | "url">[]): boolean {
  return servers.some((s) => s.name === name && !!s.url);
}
