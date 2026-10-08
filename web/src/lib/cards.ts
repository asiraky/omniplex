// What the cards an agent raises through `omniplex mcp` mean, kept apart from
// their components so the rules about what gets sent can be tested alone.
//
// One rule runs through all of it: resolve_card carries only what the user
// changed. A value the agent passed is held by the server with the card, so
// leaving its field alone is how the user keeps it, and a field that was never
// touched is never sent.

import type {
  AuthBeginArgs,
  Card,
  CardCli,
  CardEdits,
  CardKind,
  CardOutcome,
  CardResult,
  CardScope,
  CardStagedSkill,
} from "~/protocol";

/** The card's header: what kind of change it is. */
export const CARD_LABEL: Record<CardKind, string> = {
  add_mcp_server: "add MCP server",
  remove_mcp_server: "remove MCP server",
  install_skill: "install skills",
  create_skill: "new skill",
  remove_skill: "remove skill",
  add_sign_in: "add sign-in",
  add_account: "add account",
};

export const PROJECT_SCOPE = "This project";
export const EVERYWHERE = "Everywhere";

/** A thread with no project can only propose for everywhere. */
export function canPickScope(card: Card): boolean {
  return Boolean(card.projectId);
}

/** The scope the card starts on. */
export function startScope(card: Card): CardScope {
  if (!canPickScope(card)) return "everywhere";
  return card.scope ?? "project";
}

/** "project" and "everywhere" in words; anything else is already words. */
export function scopeText(scope: string | undefined): string {
  if (scope === "project") return PROJECT_SCOPE;
  if (scope === "everywhere" || !scope) return EVERYWHERE;
  return scope;
}

/** One line for a card, for when the server sent none. */
export function cardSummary(card: Card): string {
  switch (card.kind) {
    case "add_mcp_server":
      return `Add the MCP server ${card.server?.name ?? ""}`.trim();
    case "remove_mcp_server":
      return `Remove the MCP server ${card.remove?.name ?? ""}`.trim();
    case "install_skill":
      return card.staged?.source ? `Install skills from ${card.staged.source}` : "Install skills";
    case "create_skill":
      return `Create the skill ${card.skill?.name ?? ""}`.trim();
    case "remove_skill":
      return `Remove the skill ${card.remove?.name ?? ""}`.trim();
    case "add_sign_in":
      return `Add a sign-in for ${card.cli?.name ?? "a command-line tool"}`;
    case "add_account":
      return `Add the account ${card.account?.name ?? ""} to ${card.account?.cliName || card.account?.cli || "a sign-in"}`;
  }
}

// ---- add_mcp_server ----

export interface McpChoices {
  scope: CardScope;
  /** What the user typed, by name. Blank keeps a held value. */
  env: Record<string, string>;
  headers: Record<string, string>;
}

function typed(values: Record<string, string>): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(values)) {
    // A pasted key often brings a newline with it.
    const v = value.trim();
    if (v) out[name] = v;
  }
  return Object.keys(out).length ? out : undefined;
}

export function mcpEdits(card: Card, choices: McpChoices): CardEdits | undefined {
  const edits: CardEdits = {};
  if (canPickScope(card) && choices.scope !== startScope(card)) edits.scope = choices.scope;
  const env = typed(choices.env);
  if (env) edits.env = env;
  const headers = typed(choices.headers);
  if (headers) edits.headers = headers;
  return Object.keys(edits).length ? edits : undefined;
}

// ---- install_skill / create_skill ----

/** What the agent picked; the only skill there is when it picked none. */
export function startTicks(skills: CardStagedSkill[]): string[] {
  const picked = skills.filter((s) => s.picked).map((s) => s.name);
  if (picked.length === 0 && skills.length === 1) return [skills[0].name];
  return picked;
}

/** The agent's picks first, so they are not buried in a source of dozens; otherwise in source order. */
export function pickedFirst(skills: CardStagedSkill[]): CardStagedSkill[] {
  return [...skills.filter((s) => s.picked), ...skills.filter((s) => !s.picked)];
}

export function installEdits(card: Card, ticked: string[], destination: string): CardEdits | undefined {
  const edits: CardEdits = {};
  // Measured against what the server would install unasked, not against the
  // first ticks: one skill ticked here because it was the only one still has
  // to be named.
  const skills = card.staged?.skills ?? [];
  const picked = new Set(skills.filter((s) => s.picked).map((s) => s.name));
  const chosen = skills.filter((s) => ticked.includes(s.name)).map((s) => s.name);
  if (chosen.length !== picked.size || chosen.some((n) => !picked.has(n))) edits.skills = chosen;
  if (destination !== (card.destination ?? "")) edits.destination = destination;
  return Object.keys(edits).length ? edits : undefined;
}

export function createEdits(card: Card, destination: string): CardEdits | undefined {
  return destination !== (card.destination ?? "") ? { destination } : undefined;
}

// ---- add_sign_in / add_account ----

export const CLI_TEXT_FIELDS = [
  "name",
  "statusCommand",
  "signedInPattern",
  "signInCommand",
  "prepareCommand",
] as const;

type CliTextField = (typeof CLI_TEXT_FIELDS)[number];

export type CliDraft = Record<CliTextField, string> & { accountEnv: Record<string, string> };

export function cliDraft(cli: CardCli | undefined): CliDraft {
  return {
    name: cli?.name ?? "",
    statusCommand: cli?.statusCommand ?? "",
    signedInPattern: cli?.signedInPattern ?? "",
    signInCommand: cli?.signInCommand ?? "",
    prepareCommand: cli?.prepareCommand ?? "",
    accountEnv: { ...(cli?.accountEnv ?? {}) },
  };
}

export function signInEdits(card: Card, draft: CliDraft): CardEdits | undefined {
  const start = cliDraft(card.cli);
  const cli: Partial<CardCli> = {};
  for (const field of CLI_TEXT_FIELDS) {
    if (draft[field] !== start[field]) cli[field] = draft[field];
  }
  // The env goes whole or not at all: it is one map on the definition.
  if (Object.entries(draft.accountEnv).some(([k, v]) => start.accountEnv[k] !== v)) {
    cli.accountEnv = draft.accountEnv;
  }
  return Object.keys(cli).length ? { cli } : undefined;
}

export function accountEdits(card: Card, name: string): CardEdits | undefined {
  const next = name.trim();
  return next && next !== (card.account?.name ?? "") ? { name: next } : undefined;
}

// ---- what happened ----

const REMOVES: CardKind[] = ["remove_mcp_server", "remove_skill"];
const INSTALLS: CardKind[] = ["install_skill", "create_skill"];

/** The marker on a card's row: what was done with it. */
export function resultText(kind: CardKind, result: CardResult): string {
  switch (result) {
    case "pending":
      return "Waiting for you";
    case "saved":
      return REMOVES.includes(kind) ? "Removed" : INSTALLS.includes(kind) ? "Installed" : "Saved";
    case "declined":
      return REMOVES.includes(kind) ? "Kept" : "Declined";
    case "cancelled":
      return "Cancelled";
  }
}

export const LIVE_TEXT: Record<NonNullable<CardOutcome["live"]>, string> = {
  now: "In use now",
  next_turn: "From the next turn",
  next_session: "From the next session",
  after_sign_in: "In use once you sign in",
};

export const SHADOWED_TEXT = "This project's own is used here";

/** A Sign in the outcome offers, and what auth_begin is told. */
export interface CardSignIn {
  key: string;
  /** The button. */
  label: string;
  /** The dialog. */
  title: string;
  description?: string;
  begin: AuthBeginArgs;
  /**
   * A server the thread's live session took, or failed to take before it had
   * a sign-in, which has to reconnect to use it. Best effort: a harness that
   * cannot reconnect one live refuses, and its next session has it anyway.
   */
  reconnect?: { name: string; project?: string };
}

/**
 * Sign in for a saved server that needs OAuth, and one per account for a
 * saved sign-in. Nothing for anything not saved: there is nothing to sign in to.
 */
export function signInTargets(outcome: CardOutcome | undefined, origin: string): CardSignIn[] {
  if (!outcome || outcome.result !== "saved") return [];
  const out: CardSignIn[] = [];
  if (outcome.needsSignIn && outcome.server?.name) {
    const { name, project } = outcome.server;
    out.push({
      key: `mcp:${project ?? ""}:${name}`,
      label: "Sign in",
      title: `Sign in to ${name}`,
      description: "Open the sign-in page and approve. This closes by itself.",
      begin: { mcpServer: name, ...(project ? { mcpProject: project } : {}), origin },
      ...(outcome.live === "now" || outcome.live === "after_sign_in" || outcome.live === "next_session"
        ? { reconnect: { name, ...(project ? { project } : {}) } }
        : {}),
    });
  }
  const cli = outcome.cli;
  if (cli?.id) {
    // Go sends an empty slice as null.
    const accounts = cli.accounts ?? [];
    const many = accounts.length > 1;
    for (const account of accounts) {
      out.push({
        key: `cli:${cli.id}:${account}`,
        label: many ? `Sign in ${account}` : "Sign in",
        title: `Sign in ${account}`,
        description: `${cli.name || cli.id}, account ${account}`,
        begin: { cli: cli.id, account },
      });
    }
  }
  return out;
}
