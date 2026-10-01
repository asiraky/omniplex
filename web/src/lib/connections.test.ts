import { describe, expect, it } from "vitest";

import type { Cli, McpHarness, McpServer } from "~/protocol";

import {
  cliForm,
  cliSaveArgs,
  emptyServerForm,
  formFromDraft,
  formFromServer,
  harnessesFor,
  joinNames,
  newRow,
  offersSignIn,
  ownsSignIn,
  serverSaveArgs,
  serverWhere,
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
