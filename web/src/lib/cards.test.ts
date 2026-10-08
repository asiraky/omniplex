import { describe, expect, it } from "vitest";

import {
  accountEdits,
  cliDraft,
  createEdits,
  installEdits,
  mcpEdits,
  signInEdits,
  signInTargets,
  startScope,
  startTicks,
  type McpChoices,
} from "~/lib/cards";
import type { Card, CardOutcome, CardStagedSkill } from "~/protocol";

const mcpCard = (over: Partial<Card> = {}): Card => ({
  kind: "add_mcp_server",
  projectId: "p1",
  projectName: "Omniplex",
  scope: "project",
  server: {
    name: "linear",
    url: "https://mcp.linear.app/mcp",
    env: [{ name: "LINEAR_KEY", held: true }],
    headers: [{ name: "Authorization", held: false }],
  },
  ...over,
});

const choices = (over: Partial<McpChoices> = {}): McpChoices => ({
  scope: "project",
  env: {},
  headers: {},
  ...over,
});

describe("mcpEdits", () => {
  it("sends nothing when nothing was touched", () => {
    expect(mcpEdits(mcpCard(), choices())).toBeUndefined();
  });

  it("keeps a held secret by not sending it, even after Replace left an empty box", () => {
    expect(mcpEdits(mcpCard(), choices({ env: { LINEAR_KEY: "" } }))).toBeUndefined();
    expect(mcpEdits(mcpCard(), choices({ env: { LINEAR_KEY: "  \n" } }))).toBeUndefined();
  });

  it("sends a typed replacement for a held secret, trimmed", () => {
    expect(mcpEdits(mcpCard(), choices({ env: { LINEAR_KEY: " lin_new\n" } }))).toEqual({
      env: { LINEAR_KEY: "lin_new" },
    });
  });

  it("sends only the values typed, by name, headers apart from env", () => {
    const card = mcpCard({
      server: {
        name: "x",
        env: [
          { name: "A", held: true },
          { name: "B", held: false },
        ],
        headers: [{ name: "Authorization", held: false }],
      },
    });
    expect(mcpEdits(card, choices({ env: { A: "", B: "b" }, headers: { Authorization: "Bearer t" } }))).toEqual({
      env: { B: "b" },
      headers: { Authorization: "Bearer t" },
    });
  });

  it("sends the scope only when it moved off where the card started", () => {
    expect(mcpEdits(mcpCard(), choices({ scope: "project" }))).toBeUndefined();
    expect(mcpEdits(mcpCard(), choices({ scope: "everywhere" }))).toEqual({ scope: "everywhere" });

    const global = mcpCard({ scope: "everywhere" });
    expect(mcpEdits(global, choices({ scope: "everywhere" }))).toBeUndefined();
    expect(mcpEdits(global, choices({ scope: "project" }))).toEqual({ scope: "project" });
  });

  it("starts a project card with no scope on the project", () => {
    expect(startScope(mcpCard({ scope: undefined }))).toBe("project");
  });

  it("never sends a scope from a thread without a project, which can only be everywhere", () => {
    const card = mcpCard({ projectId: "", scope: "project" });
    expect(startScope(card)).toBe("everywhere");
    expect(mcpEdits(card, choices({ scope: "project" }))).toBeUndefined();
  });
});

const staged = (name: string, over: Partial<CardStagedSkill> = {}): CardStagedSkill => ({
  name,
  description: "",
  files: [{ path: "SKILL.md", size: 10 }],
  ...over,
});

const installCard = (skills: CardStagedSkill[], destination = "/repo"): Card => ({
  kind: "install_skill",
  projectId: "p1",
  staged: { id: "stg_1", source: "acme/skills", skills },
  destination,
});

describe("startTicks", () => {
  it("ticks what the agent picked", () => {
    expect(startTicks([staged("a", { picked: true }), staged("b"), staged("c", { picked: true })])).toEqual(["a", "c"]);
  });

  it("ticks the only skill there is when the agent picked none", () => {
    expect(startTicks([staged("a")])).toEqual(["a"]);
  });

  it("ticks nothing in a source of many when the agent picked none", () => {
    expect(startTicks([staged("a"), staged("b")])).toEqual([]);
  });
});

describe("installEdits", () => {
  const card = installCard([staged("a", { picked: true }), staged("b"), staged("c", { picked: true })]);

  it("sends nothing when the ticks and destination are what the agent proposed", () => {
    expect(installEdits(card, ["c", "a"], "/repo")).toBeUndefined();
  });

  it("sends the whole ticked list, in the source's order, once it differs", () => {
    expect(installEdits(card, ["c", "b", "a"], "/repo")).toEqual({ skills: ["a", "b", "c"] });
    expect(installEdits(card, ["c"], "/repo")).toEqual({ skills: ["c"] });
  });

  it("ignores a ticked name the source does not hold", () => {
    expect(installEdits(card, ["a", "c", "ghost"], "/repo")).toBeUndefined();
  });

  it("names the lone skill the agent did not pick", () => {
    expect(installEdits(installCard([staged("only")]), ["only"], "/repo")).toEqual({ skills: ["only"] });
  });

  it("sends a moved destination, including personal", () => {
    expect(installEdits(card, ["a", "c"], "/other")).toEqual({ destination: "/other" });
    expect(installEdits(card, ["a", "c"], "")).toEqual({ destination: "" });
  });

  it("treats a card with no destination as personal", () => {
    const personal = { ...card, destination: undefined };
    expect(installEdits(personal, ["a", "c"], "")).toBeUndefined();
    expect(installEdits(personal, ["a", "c"], "/repo")).toEqual({ destination: "/repo" });
  });
});

describe("createEdits", () => {
  const card: Card = { kind: "create_skill", destination: "/home/p1", skill: { name: "x", description: "d", content: "c" } };

  it("sends the destination only when it moved", () => {
    expect(createEdits(card, "/home/p1")).toBeUndefined();
    expect(createEdits(card, "")).toEqual({ destination: "" });
    expect(createEdits({ ...card, destination: "" }, "")).toBeUndefined();
  });
});

describe("signInEdits", () => {
  const card: Card = {
    kind: "add_sign_in",
    cli: {
      id: "gws",
      name: "Google Workspace",
      statusCommand: "gws auth status",
      signInCommand: "gws auth login",
      accountEnv: { GWS_CONFIG_DIR: "{dir}", GWS_PROFILE: "{account}" },
      accounts: ["work"],
    },
  };

  it("sends nothing for an untouched definition", () => {
    expect(signInEdits(card, cliDraft(card.cli))).toBeUndefined();
  });

  it("sends only the changed fields, and a field the agent left out once it is filled", () => {
    const draft = { ...cliDraft(card.cli), statusCommand: "gws auth status --json", signedInPattern: "logged in" };
    expect(signInEdits(card, draft)).toEqual({
      cli: { statusCommand: "gws auth status --json", signedInPattern: "logged in" },
    });
  });

  it("sends the per-account env whole when one entry changed", () => {
    const draft = cliDraft(card.cli);
    draft.accountEnv = { ...draft.accountEnv, GWS_PROFILE: "{account}-x" };
    expect(signInEdits(card, draft)).toEqual({
      cli: { accountEnv: { GWS_CONFIG_DIR: "{dir}", GWS_PROFILE: "{account}-x" } },
    });
  });

  it("does not share the card's env with the draft", () => {
    const draft = cliDraft(card.cli);
    draft.accountEnv.GWS_PROFILE = "changed";
    expect(card.cli?.accountEnv?.GWS_PROFILE).toBe("{account}");
  });
});

describe("accountEdits", () => {
  const card: Card = { kind: "add_account", account: { cli: "gws", cliName: "Google Workspace", name: "work" } };

  it("sends a rename, trimmed", () => {
    expect(accountEdits(card, "  personal ")).toEqual({ name: "personal" });
  });

  it("sends nothing for the proposed name, padded or not, or a blank one", () => {
    expect(accountEdits(card, "work")).toBeUndefined();
    expect(accountEdits(card, " work ")).toBeUndefined();
    expect(accountEdits(card, "   ")).toBeUndefined();
  });
});

describe("signInTargets", () => {
  const origin = "https://omniplex.test";

  it("offers the server's sign-in for a saved server that needs one", () => {
    const outcome: CardOutcome = { result: "saved", server: { name: "linear", project: "p1" }, needsSignIn: true };
    expect(signInTargets(outcome, origin).map((t) => t.begin)).toEqual([
      { mcpServer: "linear", mcpProject: "p1", origin },
    ]);
  });

  it("leaves the project out for a server saved everywhere", () => {
    const outcome: CardOutcome = { result: "saved", server: { name: "linear", project: "" }, needsSignIn: true };
    expect(signInTargets(outcome, origin).map((t) => t.begin)).toEqual([{ mcpServer: "linear", origin }]);
  });

  it("offers nothing for a saved server that does not need a sign-in", () => {
    expect(signInTargets({ result: "saved", server: { name: "linear", project: "p1" } }, origin)).toEqual([]);
  });

  it("offers one sign-in per account of a saved sign-in, each with its own key", () => {
    const targets = signInTargets({ result: "saved", cli: { id: "gws", accounts: ["work", "home"] } }, origin);
    expect(targets.map((t) => t.begin)).toEqual([
      { cli: "gws", account: "work" },
      { cli: "gws", account: "home" },
    ]);
    expect(new Set(targets.map((t) => t.key)).size).toBe(2);
  });

  it("offers nothing for a sign-in saved with no accounts, null from Go included", () => {
    expect(signInTargets({ result: "saved", cli: { id: "gws", accounts: [] } }, origin)).toEqual([]);
    const fromGo = { result: "saved", cli: { id: "gws", accounts: null } } as unknown as CardOutcome;
    expect(signInTargets(fromGo, origin)).toEqual([]);
  });

  it("offers nothing for a declined or cancelled card, or one not answered", () => {
    for (const result of ["declined", "cancelled"] as const) {
      expect(
        signInTargets({ result, server: { name: "linear" }, needsSignIn: true, cli: { id: "gws", accounts: ["work"] } }, origin),
      ).toEqual([]);
    }
    expect(signInTargets(undefined, origin)).toEqual([]);
  });
});
