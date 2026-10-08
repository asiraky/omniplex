import { describe, expect, it } from "vitest";

import type { Cli, FoundServer, McpHarness, McpServer, ThreadMcp } from "~/protocol";

import {
  cliForm,
  cliMark,
  cliMatches,
  cliSaveArgs,
  emptyServerForm,
  formFromDraft,
  foundByHarness,
  formFromServer,
  groupServers,
  harnessesFor,
  joinNames,
  BUILT_IN_MCP,
  liveAction,
  liveSource,
  newRow,
  offSummary,
  offersSignIn,
  ownsSignIn,
  sameHost,
  serverKey,
  serverMatches,
  serverSaveArgs,
  serverWhere,
  shadowing,
  sortLive,
  threadServers,
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
    offIn: [],
    oauth: false,
    status: "unchecked",
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

  it("saves an edited server in its own project, and a new one where the form says", () => {
    const edited = serverSaveArgs(formFromServer(server({ project: "p1" })), "srv");
    expect(edited).toHaveProperty("args.server.project", "p1");
    const base = { ...emptyServerForm("p2"), name: "new", url: "https://x.example/mcp" };
    expect(serverSaveArgs(base)).toHaveProperty("args.server.project", "p2");
    expect(serverSaveArgs({ ...base, project: undefined })).not.toHaveProperty("args.server.project");
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

  it("keeps two projects' servers of one name and the one everywhere apart", () => {
    const list = [server({ name: "linear" }), server({ name: "linear", project: "p1" }), server({ name: "linear", project: "p2" })];
    const next = upsert(list, server({ name: "linear", project: "p1", status: "connected" }), serverKey);
    expect(next.map((s) => [s.project, s.status])).toEqual([
      [undefined, "unchecked"],
      ["p1", "connected"],
      ["p2", "unchecked"],
    ]);
    // A rename in p2 replaces p2's only.
    const renamed = upsert(list, server({ name: "linear-b", project: "p2" }), serverKey, serverKey({ name: "linear", project: "p2" }));
    expect(renamed.map(serverKey)).toEqual(["linear", "p1/linear", "p2/linear-b"]);
  });
});

describe("groupServers", () => {
  const servers = [
    server({ name: "zeta" }),
    server({ name: "Alpha" }),
    server({ name: "sentry", project: "p1" }),
    server({ name: "db", project: "p1" }),
    server({ name: "linear", project: "p2" }),
  ];

  it("in a project, splits its own from the ones everywhere, each by name", () => {
    const g = groupServers(servers, "p1");
    expect(g.here.map((s) => s.name)).toEqual(["db", "sentry"]);
    expect(g.everywhere.map((s) => s.name)).toEqual(["Alpha", "zeta"]);
    expect(g.projects).toEqual([]);
  });

  it("outside a project, puts every project's servers in a group of their own", () => {
    const g = groupServers(servers, undefined);
    expect(g.here).toEqual([]);
    expect(g.everywhere.map((s) => s.name)).toEqual(["Alpha", "zeta"]);
    expect(g.projects.map((p) => [p.project, p.servers.map((s) => s.name)])).toEqual([
      ["p1", ["db", "sentry"]],
      ["p2", ["linear"]],
    ]);
  });
});

describe("shadowing", () => {
  const servers = [
    server({ name: "linear" }),
    server({ name: "gmail" }),
    server({ name: "linear", project: "p1" }),
    server({ name: "sentry", project: "p1" }),
    server({ name: "gmail", project: "p2" }),
  ];

  it("marks both sides of a project server named like one everywhere, in that project only", () => {
    const [linear, gmail, p1Linear, sentry] = servers;
    expect(shadowing(p1Linear, servers, "p1")).toBe("replaces");
    expect(shadowing(linear, servers, "p1")).toBe("replaced");
    expect(shadowing(sentry, servers, "p1")).toBeNull();
    // p2's gmail replaces gmail in p2, not in p1.
    expect(shadowing(gmail, servers, "p1")).toBeNull();
    expect(shadowing(gmail, servers, "p2")).toBe("replaced");
    expect(shadowing(linear, servers, undefined)).toBeNull();
  });
});

describe("threadServers", () => {
  const linear = server({ name: "linear", url: "https://mcp.linear.app/mcp" });
  const gmail = server({ name: "gmail", offIn: ["p1"] });
  const p1Linear = server({ name: "linear", project: "p1", url: "https://client.example/mcp" });
  const p2Db = server({ name: "db", project: "p2" });
  const servers = [linear, gmail, p1Linear, p2Db];

  it("gives a project's thread its own, then the ones everywhere it has not replaced or turned off", () => {
    expect(threadServers(servers, "p1")).toEqual([p1Linear]);
    expect(threadServers(servers, "p2")).toEqual([p2Db, linear, gmail]);
  });

  it("gives a thread with no project the ones everywhere only", () => {
    expect(threadServers(servers, undefined)).toEqual([linear, gmail]);
  });

  it("resolves the thread's report to the project's server when it shadows one everywhere", () => {
    // The project's linear is a command; the one everywhere is remote. A
    // project thread must not be offered a sign-in that belongs to the other.
    const local = server({ name: "linear", project: "p1", url: undefined, command: "./linear" });
    const ours = threadServers([linear, local], "p1");
    expect(liveAction({ name: "linear", status: "needs_auth" }, ours)).toBe("reconnect");
    expect(liveAction({ name: "linear", status: "needs_auth" }, threadServers([linear, local], undefined))).toBe("sign_in");
    expect(ours.find((s) => s.name === "linear")?.project).toBe("p1");
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

describe("sameHost", () => {
  it("names the other servers on this one's host, whatever the path", () => {
    const servers = [
      server({ name: "cf-work", url: "https://mcp.cloudflare.com/mcp" }),
      server({ name: "cf-home", url: "https://mcp.cloudflare.com/sse" }),
      server({ name: "gh", url: "https://api.github.com/mcp" }),
      server({ name: "local", url: undefined, command: "x" }),
      server({ name: "broken", url: "not a url" }),
    ];
    const names = (target: McpServer) => sameHost(target, servers).map((s) => s.name);
    expect(names(servers[0])).toEqual(["cf-home"]);
    expect(names(servers[2])).toEqual([]);
    expect(names(servers[3])).toEqual([]);
    expect(names(servers[4])).toEqual([]);
  });

  it("counts a server of the same name in another scope as another login", () => {
    const mine = server({ name: "linear", url: "https://mcp.linear.app/mcp" });
    const client = server({ name: "linear", project: "p1", url: "https://mcp.linear.app/mcp" });
    expect(sameHost(client, [mine, client])).toEqual([mine]);
    expect(sameHost(mine, [mine, client])).toEqual([client]);
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
