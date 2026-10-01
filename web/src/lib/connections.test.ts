import { describe, expect, it } from "vitest";

import type { Cli, FoundServer, McpHarness, McpServer, McpServerAccount, ThreadMcp } from "~/protocol";

import {
  accountForm,
  accountSaveArgs,
  cliForm,
  cliMark,
  cliMatches,
  cliSaveArgs,
  emptyServerForm,
  formFromDraft,
  foundByHarness,
  formFromServer,
  harnessesFor,
  joinNames,
  BUILT_IN_MCP,
  liveAction,
  liveSource,
  newRow,
  offSummary,
  offersSignIn,
  ownsSignIn,
  serverMark,
  serverMatches,
  serverSaveArgs,
  sessionServers,
  serverWhere,
  sortLive,
  toggleOff,
  upsert,
} from "./connections";

const agents: McpHarness[] = [
  { id: "a", name: "Alpha", transports: ["stdio", "http"] },
  { id: "b", name: "Beta", transports: ["stdio"] },
  { id: "c", name: "Gamma", transports: ["http"] },
];

function server(over: Partial<McpServer> = {}): McpServer {
  return {
    name: "srv",
    url: "https://mcp.example.com/mcp",
    envNames: [],
    headerNames: [],
    off: [],
    oauth: false,
    status: "unchecked",
    accounts: [],
    ...over,
  };
}

describe("harnessesFor", () => {
  it("offers only the agents that run that kind of server, whatever they are", () => {
    expect(harnessesFor(agents, "http").map((h) => h.id)).toEqual(["a", "c"]);
    expect(harnessesFor(agents, "stdio").map((h) => h.id)).toEqual(["a", "b"]);
  });
});

describe("toggleOff", () => {
  it("adds and removes one agent, keeping entries for agents not on screen", () => {
    expect(toggleOff(["gone"], "a", false)).toEqual(["gone", "a"]);
    expect(toggleOff(["gone", "a"], "a", true)).toEqual(["gone"]);
    expect(toggleOff(["a"], "a", false)).toEqual(["a"]);
  });
});

describe("serverSaveArgs", () => {
  it("keeps a stored header left blank, sends a typed one, and drops a removed one", () => {
    const form = formFromServer(server({ headerNames: ["Keep", "Replace", "Drop"] }));
    form.headers = form.headers
      .filter((r) => r.name !== "Drop")
      .map((r) => (r.name === "Replace" ? { ...r, value: "new" } : r));
    const built = serverSaveArgs(form, "srv");
    expect(built).toEqual({
      args: {
        server: { name: "srv", url: "https://mcp.example.com/mcp", env: {}, headers: { Keep: "", Replace: "new" }, off: [] },
        previousName: "srv",
      },
    });
  });

  it("refuses a new value with nothing in it rather than sending a keep marker", () => {
    const form = { ...emptyServerForm(), name: "srv", url: "https://x.example/mcp", headers: [newRow("Auth", "")] };
    expect(serverSaveArgs(form)).toHaveProperty("error");
  });

  it("ignores fully blank rows and rejects duplicates", () => {
    const base = { ...emptyServerForm(), name: "srv", url: "https://x.example/mcp" };
    expect(serverSaveArgs({ ...base, headers: [newRow()] })).toHaveProperty("args.server.headers", {});
    expect(
      serverSaveArgs({ ...base, headers: [newRow("A", "1"), newRow("A", "2")] }),
    ).toHaveProperty("error");
  });

  it("sends a command server's arguments one per line and its env, not headers", () => {
    const form = formFromDraft({
      name: "local",
      command: "npx",
      args: ["-y", "pkg with space"],
      env: { TOKEN: "t" },
      headers: {},
    });
    const built = serverSaveArgs({ ...form, off: ["b"] });
    expect(built).toEqual({
      args: {
        server: { name: "local", command: "npx", args: ["-y", "pkg with space"], env: { TOKEN: "t" }, headers: {}, off: ["b"] },
      },
    });
  });

  it("rejects bad names, the reserved name, and a missing address", () => {
    const ok = { ...emptyServerForm(), name: "srv", url: "https://x.example/mcp" };
    expect(serverSaveArgs({ ...ok, name: "Bad Name" })).toHaveProperty("error");
    expect(serverSaveArgs({ ...ok, name: "omniplex" })).toHaveProperty("error");
    expect(serverSaveArgs({ ...ok, url: "" })).toHaveProperty("error");
    expect(serverSaveArgs({ ...ok, url: "ftp://x" })).toHaveProperty("error");
    expect(serverSaveArgs({ ...ok, kind: "stdio" })).toHaveProperty("error");
  });
});

describe("offersSignIn", () => {
  it("is for remote servers not signed in through Omniplex, or whose sign-in stopped working", () => {
    expect(offersSignIn(server({ status: "sign_in" }))).toBe(true);
    expect(offersSignIn(server({ status: "sign_in", oauth: true }))).toBe(true);
    expect(offersSignIn(server({ status: "failed", oauth: true }))).toBe(false);
    expect(offersSignIn(server({ status: "connected" }))).toBe(false);
    expect(offersSignIn(server({ url: undefined, command: "x", status: "failed" }))).toBe(false);
  });
});

describe("cliSaveArgs", () => {
  const form = {
    ...cliForm(),
    name: "Google Workspace",
    statusCommand: "gws status",
    signInCommand: "gws login",
    accountEnv: [newRow("DIR", "~/.config/gws-{account}")],
  };

  it("derives a fresh id for a new sign-in, clear of taken ones", () => {
    const built = cliSaveArgs(form, undefined, ["google-workspace"]);
    expect(built).toMatchObject({ args: { cli: { id: "google-workspace-2", accounts: [], accountEnv: { DIR: "~/.config/gws-{account}" } } } });
    expect(built).not.toHaveProperty("args.previousId");
  });

  it("keeps an existing sign-in's id and accounts, without their statuses", () => {
    const existing: Cli = {
      id: "gws",
      name: "Old",
      statusCommand: "",
      signedInPattern: "",
      signInCommand: "",
      prepareCommand: "",
      accountEnv: {},
      accounts: [{ name: "work", env: { DIR: "/w" }, status: "signed_in", detail: "x" }],
    };
    expect(cliSaveArgs(form, existing, ["gws"])).toMatchObject({
      args: { cli: { id: "gws", accounts: [{ name: "work", env: { DIR: "/w" } }] }, previousId: "gws" },
    });
    const accounts = (cliSaveArgs(form, existing, []) as { args: { cli: Cli } }).args.cli.accounts;
    expect(accounts[0]).not.toHaveProperty("status");
  });

  it("needs the commands it will run", () => {
    expect(cliSaveArgs({ ...form, statusCommand: " " }, undefined, [])).toHaveProperty("error");
    expect(cliSaveArgs({ ...form, signInCommand: "" }, undefined, [])).toHaveProperty("error");
  });
});

describe("upsert", () => {
  const key = (s: { name: string }) => s.name;
  it("replaces under the previous key on a rename, and appends something new", () => {
    const list = [{ name: "a", v: 1 }, { name: "b", v: 1 }];
    expect(upsert(list, { name: "c", v: 2 }, key, "a")).toEqual([{ name: "c", v: 2 }, { name: "b", v: 1 }]);
    expect(upsert(list, { name: "d", v: 2 }, key)).toHaveLength(3);
  });
});

describe("ownsSignIn", () => {
  it("is true only for one of our remote servers", () => {
    const ours = [server({ name: "remote" }), server({ name: "local", url: undefined, command: "x" })];
    expect(ownsSignIn("remote", ours)).toBe(true);
    expect(ownsSignIn("local", ours)).toBe(false);
    expect(ownsSignIn("theirs", ours)).toBe(false);
  });
});

describe("serverWhere and joinNames", () => {
  it("shows a URL's host and a command with its arguments", () => {
    expect(serverWhere({ url: "https://mcp.example.com:8443/mcp" })).toBe("mcp.example.com:8443");
    expect(serverWhere({ command: "npx", args: ["-y", "pkg"] })).toBe("npx -y pkg");
  });

  it("joins any number of names", () => {
    expect(joinNames([])).toBe("");
    expect(joinNames(["A"])).toBe("A");
    expect(joinNames(["A", "B"])).toBe("A and B");
    expect(joinNames(["A", "B", "C"])).toBe("A, B and C");
  });
});

describe("offSummary", () => {
  it("says nothing when every able agent has it, Off for none, and names the ones left otherwise", () => {
    // Gamma cannot run a command server, so turning it off changes nothing.
    expect(offSummary(server({ url: undefined, command: "x", off: ["c"] }), agents)).toBeNull();
    expect(offSummary(server({ off: ["a", "c"] }), agents)).toBe("Off");
    expect(offSummary(server({ off: ["a"] }), agents)).toBe("Only Gamma");
    expect(offSummary(server(), [])).toBeNull();
  });
});

describe("cliMark", () => {
  it("speaks for the worst account and says nothing when all are signed in", () => {
    expect(cliMark([])?.tone).toBe("quiet");
    expect(cliMark([{ status: "signed_in" }, { status: "signed_in" }])).toBeNull();
    expect(cliMark([{ status: "signed_in" }, { status: "unchecked" }])).toBeNull();
    expect(cliMark([{ status: "signed_out" }, { status: "signed_in" }])?.tone).toBe("attention");
    expect(cliMark([{ status: "signed_out" }, { status: "failed" }, { status: "signed_in" }])?.tone).toBe("bad");
  });
});

describe("serverMatches and cliMatches", () => {
  it("match on a name or where it runs, ignoring case and blank queries", () => {
    const s = server({ name: "docs", url: "https://Search.example.com/mcp" });
    expect(serverMatches(s, "  ")).toBe(true);
    expect(serverMatches(s, "DOC")).toBe(true);
    expect(serverMatches(s, "search.example")).toBe(true);
    expect(serverMatches(s, "nope")).toBe(false);
    const c: Cli = {
      id: "gws",
      name: "Google",
      statusCommand: "",
      signedInPattern: "",
      signInCommand: "",
      prepareCommand: "",
      accountEnv: {},
      accounts: [{ name: "work", env: {}, status: "unchecked" }],
    };
    expect(cliMatches(c, "goo")).toBe(true);
    expect(cliMatches(c, "WORK")).toBe(true);
    expect(cliMatches(c, "home")).toBe(false);
  });
});

describe("foundByHarness", () => {
  const found = (name: string, harness: string): FoundServer => ({ name, harness, origin: "", added: false });

  it("groups in agent order, skips agents with none, and keeps an unknown agent last", () => {
    const groups = foundByHarness([found("x", "zz"), found("y", "c"), found("z", "a"), found("w", "c")], agents);
    expect(groups.map((g) => [g.harness.id, g.servers.map((s) => s.name)])).toEqual([
      ["a", ["z"]],
      ["c", ["y", "w"]],
      ["zz", ["x"]],
    ]);
    expect(groups[2].harness.name).toBe("zz");
  });
});

describe("sortLive", () => {
  it("puts problems first, then sorts by name ignoring case", () => {
    const live: ThreadMcp[] = [
      { name: "b", status: "connected" },
      { name: "A", status: "connected" },
      { name: "c", status: "needs_auth" },
      { name: "d", status: "failed" },
      { name: "e", status: "pending" },
    ];
    expect(sortLive(live).map((s) => s.name)).toEqual(["d", "c", "e", "A", "b"]);
    // The input is left alone.
    expect(live[0].name).toBe("b");
  });
});

describe("liveAction", () => {
  const ours = [server({ name: "remote" }), server({ name: "local", url: undefined, command: "x" })];

  it("offers sign in only for our remote server that wants it", () => {
    expect(liveAction({ name: "remote", status: "needs_auth" }, ours)).toBe("sign_in");
    expect(liveAction({ name: "local", status: "needs_auth" }, ours)).toBe("reconnect");
  });

  it("offers reconnect for our failed server and nothing to fix for theirs", () => {
    expect(liveAction({ name: "remote", status: "failed" }, ours)).toBe("reconnect");
    expect(liveAction({ name: "other", status: "failed" }, ours)).toBe("theirs");
    expect(liveAction({ name: "other", status: "needs_auth" }, ours)).toBe("theirs");
  });

  it("offers nothing for a server that is fine or still starting", () => {
    expect(liveAction({ name: "remote", status: "connected" }, ours)).toBeNull();
    expect(liveAction({ name: "other", status: "pending" }, ours)).toBeNull();
    expect(liveAction({ name: "remote", status: "disabled" }, ours)).toBeNull();
  });

  it("offers nothing for the built-in server, which is neither ours to fix nor theirs", () => {
    expect(liveAction({ name: BUILT_IN_MCP, status: "failed" }, ours)).toBeNull();
    expect(liveSource(BUILT_IN_MCP, ours)).toBe("built_in");
    expect(liveSource("remote", ours)).toBe("ours");
    expect(liveSource("other", ours)).toBe("theirs");
  });
});

function account(over: Partial<McpServerAccount> = {}): McpServerAccount {
  return { label: "work", name: "srv-work", envNames: [], headerNames: [], oauth: false, status: "unchecked", ...over };
}

describe("server accounts", () => {
  const srv = server({
    headerNames: ["X-Key", "X-Team"],
    accounts: [account({ headerNames: ["X-Key"] }), account({ label: "home", name: "srv-home" })],
  });

  it("sends a typed value, keeps a blank own one, and leaves the rest to the server", () => {
    const form = accountForm(srv, srv.accounts[0]);
    expect(form.values).toEqual([
      { name: "X-Key", value: "", own: true },
      { name: "X-Team", value: "", own: false },
    ]);
    expect(accountSaveArgs(form, srv, srv.accounts[0])).toEqual({
      args: { server: "srv", account: { label: "work", env: {}, headers: { "X-Key": "" } }, previousLabel: "work" },
    });

    const cleared = { ...form, values: [{ name: "X-Key", value: "", own: false }, { name: "X-Team", value: "t2", own: false }] };
    expect(accountSaveArgs(cleared, srv, srv.accounts[0])).toMatchObject({ args: { account: { headers: { "X-Team": "t2" } } } });
  });

  it("puts a command server's values in env", () => {
    const local = server({ url: undefined, command: "x", envNames: ["TOKEN"], headerNames: [], accounts: [] });
    const form = accountForm(local);
    form.label = "b";
    form.values[0]!.value = "v";
    expect(accountSaveArgs(form, local)).toEqual({ args: { server: "srv", account: { label: "b", env: { TOKEN: "v" }, headers: {} } } });
  });

  it("refuses labels that would not make a server name, or that the server already has", () => {
    const tryLabel = (label: string, previous?: McpServerAccount) =>
      "error" in accountSaveArgs({ ...accountForm(srv), label }, srv, previous);
    expect(tryLabel("")).toBe(true);
    expect(tryLabel("Work")).toBe(true);
    expect(tryLabel("x".repeat(45))).toBe(true);
    expect(tryLabel("x".repeat(44))).toBe(false);
    expect(tryLabel("home")).toBe(true);
    // Saving an account under its own label is no clash.
    expect(tryLabel("work", srv.accounts[0])).toBe(false);
    expect(tryLabel("home", srv.accounts[0])).toBe(true);
  });

  it("marks a server's row for the worst of its accounts", () => {
    expect(serverMark(srv)).toBeNull();
    expect(serverMark({ ...srv, accounts: [] , status: "sign_in" })?.label).toBe("Sign in");
    expect(serverMark({ ...srv, accounts: [account({ status: "sign_in" })] })?.label).toBe("1 needs sign-in");
    expect(serverMark({ ...srv, status: "sign_in", accounts: [account({ status: "sign_in" })] })?.label).toBe("2 need sign-in");
    expect(serverMark({ ...srv, status: "sign_in", accounts: [account({ status: "failed" })] })?.label).toBe("Failed");
  });

  it("knows an account in a thread's report as ours, with the server it belongs to", () => {
    const ours = sessionServers([srv, server({ name: "local", url: undefined, command: "x", accounts: [account({ name: "local-b" })] })]);
    expect(ours.map((o) => `${o.name}>${o.server}`)).toEqual(["srv>srv", "srv-work>srv", "srv-home>srv", "local>local", "local-b>local"]);
    expect(liveSource("srv-home", ours)).toBe("ours");
    expect(liveAction({ name: "srv-home", status: "needs_auth" }, ours)).toBe("sign_in");
    expect(liveAction({ name: "local-b", status: "needs_auth" }, ours)).toBe("reconnect");
  });
});
