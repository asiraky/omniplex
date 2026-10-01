// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ToolsTab } from "~/lib/toolsTab";
import type { Cli, Connections, McpServer, ThreadMcpReport } from "~/protocol";
import { render } from "~/test/harness";

import { ToolsPage } from "./ToolsPage";

const server = (over: Partial<McpServer> = {}): McpServer => ({
  name: "srv",
  url: "https://mcp.example.com/mcp",
  envNames: [],
  headerNames: [],
  off: [],
  oauth: false,
  status: "connected",
  ...over,
});

const cli = (over: Partial<Cli> = {}): Cli => ({
  id: "gws",
  name: "Google Workspace",
  statusCommand: "gws auth status",
  signedInPattern: "",
  signInCommand: "gws auth login",
  prepareCommand: "",
  accountEnv: {},
  accounts: [],
  ...over,
});

type Handler = (args: Record<string, unknown>) => unknown;

function backend(overrides: Record<string, Handler> = {}, live: ThreadMcpReport = { live: true, servers: [] }) {
  const state: Connections = {
    harnesses: [
      { id: "claude", name: "Claude", transports: ["stdio", "http"] },
      { id: "codex", name: "Codex", transports: ["stdio", "http"] },
    ],
    servers: [server({ name: "docs" }), server({ name: "local", url: undefined, command: "./run" })],
    found: [],
    clis: [],
  };
  const handlers: Record<string, Handler> = {
    list_skills: () => ({ skills: [], claudeSync: true, codexBundled: true }),
    list_connections: () => state,
    check_mcp_server: (args) => ({ server: state.servers.find((s) => s.name === args.name) }),
    check_cli: (args) => ({ cli: state.clis.find((c) => c.id === args.id) }),
    thread_mcp_status: () => live,
    ...overrides,
  };
  const command = vi.fn(async (name: string, args: unknown) => {
    const handler = handlers[name];
    if (!handler) throw new Error(`unexpected command ${name}`);
    return handler(args as Record<string, unknown>);
  });
  return { command, state };
}

const calls = (command: Command, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args as Record<string, unknown>);

type Command = ReturnType<typeof backend>["command"];

function renderPage(command: Command, tab: ToolsTab, threadId?: string) {
  return render(
    <ToolsPage
      wires={{ command, subscribe: () => () => {} }}
      scope={threadId ? { kind: "thread", threadId } : { kind: "personal" }}
      tab={tab}
      onClose={() => {}}
    />,
  );
}

const liveSection = () => screen.getByRole("region", { name: "This thread" });

beforeEach(() => localStorage.clear());

describe("the tabs", () => {
  it("read the list once, however often the tabs are switched", async () => {
    const { command } = backend();
    renderPage(command, "skills");
    expect(calls(command, "list_connections")).toHaveLength(0);

    fireEvent.click(screen.getByRole("tab", { name: /^MCP/ }));
    await screen.findByRole("button", { name: /^docs\b/ });
    fireEvent.click(screen.getByRole("tab", { name: /^Sign-ins/ }));
    fireEvent.click(screen.getByRole("tab", { name: /^Skills/ }));
    fireEvent.click(screen.getByRole("tab", { name: /^MCP/ }));
    expect(calls(command, "list_connections")).toHaveLength(1);
    expect(calls(command, "list_skills")).toHaveLength(1);
  });
});

describe("the thread's live view", () => {
  it("offers reconnect only for our failed server, and shows the answer", async () => {
    const { command } = backend(
      {
        thread_mcp_reconnect: () => ({
          live: true,
          servers: [
            { name: "docs", status: "connected" },
            { name: "theirs", status: "failed", error: "boom" },
          ],
        }),
      },
      {
        live: true,
        servers: [
          { name: "docs", status: "failed", error: "refused" },
          { name: "theirs", status: "failed", error: "boom" },
          { name: "local", status: "connected" },
        ],
      },
    );
    renderPage(command, "mcp", "t1");
    const reconnects = await waitFor(() => {
      const found = within(liveSection()).getAllByRole("button", { name: "Reconnect" });
      expect(found).toHaveLength(1);
      return found;
    });
    await act(async () => fireEvent.click(reconnects[0]));
    expect(calls(command, "thread_mcp_reconnect")).toEqual([{ threadId: "t1", name: "docs" }]);
    await waitFor(() => expect(within(liveSection()).queryByRole("button", { name: "Reconnect" })).toBeNull());
    // Asked once on open; the reconnect's answer stands in for a second ask.
    expect(calls(command, "thread_mcp_status")).toHaveLength(1);
  });

  it("keeps a failed reconnect with its row and the rest of the report", async () => {
    const { command } = backend(
      {
        thread_mcp_reconnect: () => {
          throw new Error("still down");
        },
      },
      {
        live: true,
        servers: [
          { name: "docs", status: "failed", error: "refused" },
          { name: "local", status: "connected" },
        ],
      },
    );
    renderPage(command, "mcp", "t1");
    const reconnect = await waitFor(() => within(liveSection()).getByRole("button", { name: "Reconnect" }));
    await act(async () => fireEvent.click(reconnect));
    const problem = within(liveSection()).getByRole("button", { name: /still down/ });
    expect(problem.getAttribute("aria-expanded")).toBe("false");
    expect(within(liveSection()).getByRole("button", { name: /^local\b/ })).toBeTruthy();
    // Asking again clears it.
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Ask the agent again" })));
    await waitFor(() => expect(within(liveSection()).queryByRole("button", { name: /still down/ })).toBeNull());
  });

  it("offers sign in only for our remote server", async () => {
    const { command } = backend(
      {},
      {
        live: true,
        servers: [
          { name: "docs", status: "needs_auth" },
          { name: "local", status: "needs_auth" },
          { name: "theirs", status: "needs_auth" },
        ],
      },
    );
    renderPage(command, "mcp", "t1");
    await waitFor(() => expect(within(liveSection()).getAllByRole("button", { name: "Sign in" })).toHaveLength(1));
    // The command server can only be retried; theirs gets nothing.
    expect(within(liveSection()).getAllByRole("button", { name: "Reconnect" })).toHaveLength(1);
  });

  it("lists no servers without a running session, and asks again only on refresh", async () => {
    const { command } = backend({}, { live: false, servers: [] });
    renderPage(command, "mcp", "t1");
    await screen.findByRole("button", { name: /^docs\b/ });
    await waitFor(() => expect(within(liveSection()).queryByRole("list")).toBeNull());

    fireEvent.click(screen.getByRole("tab", { name: /^Sign-ins/ }));
    fireEvent.click(screen.getByRole("tab", { name: /^MCP/ }));
    expect(calls(command, "thread_mcp_status")).toHaveLength(1);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Ask the agent again" })));
    expect(calls(command, "thread_mcp_status")).toHaveLength(2);
  });

  it("is not there without a thread", async () => {
    const { command } = backend();
    renderPage(command, "mcp");
    await screen.findByRole("button", { name: /^docs\b/ });
    expect(screen.queryByRole("region", { name: "This thread" })).toBeNull();
    expect(calls(command, "thread_mcp_status")).toHaveLength(0);
  });
});

describe("found servers", () => {
  it("are added by where they run, and then read as added", async () => {
    const { command, state } = backend({
      add_found_server: (args) => ({ server: server({ name: String(args.name), url: String(args.where) }) }),
    });
    state.found = [
      { name: "search", harness: "codex", origin: "~/.codex/config.toml", url: "https://a.example/mcp", added: false },
      { name: "search", harness: "codex", origin: "./.codex/config.toml", url: "https://b.example/mcp", added: false },
    ];
    renderPage(command, "mcp");
    fireEvent.click(await screen.findByRole("button", { name: /Found in Codex/ }));
    const found = within(screen.getByRole("region", { name: "Found in Codex" })).getAllByRole("button", { name: /^search\b/ });
    fireEvent.click(found[1]);

    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add to Omniplex" })));
    expect(calls(command, "add_found_server")).toEqual([{ harness: "codex", name: "search", where: "https://b.example/mcp" }]);

    fireEvent.click(await screen.findByRole("button", { name: "Open search" }));
    await screen.findByRole("heading", { name: "search" });
  });
});

describe("adding a server", () => {
  it("reads the paste, then saves what the form holds and opens it", async () => {
    const { command } = backend({
      parse_mcp_server: () => ({ draft: { name: "notes", url: "https://notes.example/mcp" } }),
      save_mcp_server: (args) => ({ server: { ...server(), ...(args.server as object) } }),
    });
    renderPage(command, "mcp");
    await screen.findByRole("button", { name: /^docs\b/ });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "https://notes.example/mcp" } });
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "Continue" })));
    expect(calls(command, "parse_mcp_server")).toEqual([{ text: "https://notes.example/mcp" }]);

    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "Add server" })));
    expect(calls(command, "save_mcp_server")[0]).toMatchObject({ server: { name: "notes", url: "https://notes.example/mcp" } });
    await screen.findByRole("heading", { name: "notes" });
  });

  it("keeps the paste and says why when nothing in it is a server", async () => {
    const { command } = backend({ parse_mcp_server: () => ({}) });
    renderPage(command, "mcp");
    await screen.findByRole("button", { name: /^docs\b/ });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "hello" } });
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "Continue" })));
    expect(within(dialog).getByRole("textbox")).toHaveProperty("value", "hello");
    expect(within(dialog).queryByRole("button", { name: "Add server" })).toBeNull();
  });
});

describe("a server's agents", () => {
  it("switch at once and switch back when the save fails", async () => {
    let fail = false;
    const { command } = backend({
      set_mcp_server_off: (args) => {
        if (fail) throw new Error("disk full");
        return { server: server({ name: "docs", off: args.off as string[] }) };
      },
    });
    renderPage(command, "mcp");
    fireEvent.click(await screen.findByRole("button", { name: /^docs\b/ }));
    const codex = await screen.findByRole("switch", { name: "Codex" });

    await act(async () => fireEvent.click(codex));
    expect(calls(command, "set_mcp_server_off")).toEqual([{ name: "docs", off: ["codex"] }]);
    expect(codex.getAttribute("aria-checked")).toBe("false");

    fail = true;
    await act(async () => fireEvent.click(codex));
    expect(calls(command, "set_mcp_server_off")[1]).toEqual({ name: "docs", off: [] });
    expect(codex.getAttribute("aria-checked")).toBe("false");
  });
});

describe("sign-ins", () => {
  it("add an account, and remove one only after asking", async () => {
    const { command, state } = backend({
      add_cli_account: (args) => {
        const c = state.clis[0];
        state.clis = [{ ...c, accounts: [...c.accounts, { name: String(args.account), env: {}, status: "unchecked" }] }];
        return { cli: state.clis[0] };
      },
      check_cli: () => {
        const c = state.clis[0];
        state.clis = [{ ...c, accounts: c.accounts.map((a) => (a.status === "unchecked" ? { ...a, status: "signed_out" } : a)) }];
        return { cli: state.clis[0] };
      },
      remove_cli_account: (args) => {
        const c = state.clis[0];
        state.clis = [{ ...c, accounts: c.accounts.filter((a) => a.name !== args.account) }];
        return { cli: state.clis[0] };
      },
    });
    state.clis = [cli({ accounts: [{ name: "home", env: {}, status: "signed_in" }] })];
    renderPage(command, "signins");
    fireEvent.click(await screen.findByRole("button", { name: /^Google Workspace\b/ }));

    fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
    // A name the server would refuse is caught before it is sent.
    fireEvent.change(screen.getByLabelText("Account name"), { target: { value: "bad name" } });
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add" })));
    expect(calls(command, "add_cli_account")).toHaveLength(0);

    fireEvent.change(screen.getByLabelText("Account name"), { target: { value: "work" } });
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add" })));
    expect(calls(command, "add_cli_account")).toEqual([{ id: "gws", account: "work" }]);
    const accounts = screen.getByRole("region", { name: "Accounts" });
    await within(accounts).findByText("work");
    // A new account is checked straight away, not left unchecked.
    await waitFor(() => expect(calls(command, "check_cli")).toHaveLength(1));
    expect(within(screen.getByRole("tablist")).getByRole("tab", { name: /needs attention/ })).toBeTruthy();

    fireEvent.click(within(accounts).getByRole("button", { name: "Remove home" }));
    expect(calls(command, "remove_cli_account")).toHaveLength(0);
    await act(async () => fireEvent.click(await screen.findByRole("button", { name: "Remove account" })));
    expect(calls(command, "remove_cli_account")).toEqual([{ id: "gws", account: "home" }]);
    await waitFor(() => expect(within(accounts).queryByText("home")).toBeNull());
  });
});
