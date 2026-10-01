// The logic behind Settings → Connections and the thread's MCP surface, kept
// out of the components so the forms stay dumb. The one security-sensitive
// rule lives here: a stored header or env value is never echoed, so a form
// row for it starts blank, and blank on save means "keep what is stored".

import { deriveInstanceId } from "~/lib/providerSpec";
import type {
  Cli,
  CliSpec,
  McpDraft,
  McpHarness,
  McpKind,
  McpServer,
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

export interface SaveServerArgs {
  server: McpDraft & { off: string[] };
  previousName?: string;
}

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
export function offersSignIn(s: McpServer): boolean {
  if (!s.url) return false;
  return s.status === "sign_in" || (!s.oauth && s.status !== "connected");
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

// ---- the thread surface ----

/** The session can be asked to try a server again. */
export function canReconnect(status: ThreadMcpStatus): boolean {
  return status === "needs_auth" || status === "failed";
}

/** Omniplex holds the sign-in for this server: it is ours, and remote. */
export function ownsSignIn(name: string, servers: Pick<McpServer, "name" | "url">[]): boolean {
  return servers.some((s) => s.name === name && !!s.url);
}
