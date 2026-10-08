// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Connections, McpServer } from "~/protocol";

import { useConnections } from "./useConnections";

function server(over: Partial<McpServer> = {}): McpServer {
  return {
    name: "docs",
    url: "https://docs.example.com/mcp",
    envNames: [],
    headerNames: [],
    off: [],
    offIn: [],
    oauth: false,
    status: "unchecked",
    ...over,
  };
}

/** A command whose checks answer only when the test says so. */
function slowChecks(list: Connections) {
  const pending = new Map<string, (v: unknown) => void>();
  const command = <T,>(name: string, args: Record<string, unknown>): Promise<T> => {
    if (name === "list_connections") return Promise.resolve(list as T);
    return new Promise<T>((resolve) => pending.set(String(args.name), resolve as (v: unknown) => void));
  };
  return { command, answer: (name: string, v: unknown) => pending.get(name)?.(v) };
}

/** A command that answers a list per project and records every call. */
function perProject(lists: Record<string, Connections>) {
  const calls: [string, Record<string, unknown>][] = [];
  const command = <T,>(name: string, args: Record<string, unknown>): Promise<T> => {
    calls.push([name, args]);
    if (name === "list_connections") return Promise.resolve(lists[String(args.projectId ?? "")] as T);
    return Promise.resolve({} as T);
  };
  return { command, calls };
}

describe("useConnections", () => {
  it("drops a check's answer when its server was removed or edited while it ran, and keeps the rest", async () => {
    const list: Connections = {
      harnesses: [],
      servers: [server({ name: "a" }), server({ name: "b" }), server({ name: "c" })],
      found: [],
      clis: [],
    };
    const { command, answer } = slowChecks(list);
    const { result } = renderHook(() => useConnections(command, true));
    await waitFor(() => expect(result.current.conn?.servers).toHaveLength(3));

    act(() => {
      result.current.removeServer({ name: "a" });
      result.current.putServer(server({ name: "b", url: "https://new.example.com/mcp" }));
    });
    await act(async () => {
      answer("a", { server: server({ name: "a", status: "connected" }) });
      answer("b", { server: server({ name: "b", status: "connected" }) });
      answer("c", { server: server({ name: "c", status: "connected" }) });
      await Promise.resolve();
    });

    const servers = result.current.conn?.servers ?? [];
    expect(servers.map((s) => s.name)).toEqual(["b", "c"]);
    expect(servers[0]).toMatchObject({ url: "https://new.example.com/mcp", status: "unchecked" });
    expect(servers[1]?.status).toBe("connected");
  });

  it("reads the project in view's list, again when it changes, and checks the new servers by key", async () => {
    const lists: Record<string, Connections> = {
      p1: { harnesses: [], servers: [server({ name: "linear" }), server({ name: "linear", project: "p1" })], found: [], clis: [] },
      p2: { harnesses: [], servers: [server({ name: "linear" }), server({ name: "linear", project: "p2" })], found: [], clis: [] },
    };
    const { command, calls } = perProject(lists);
    const { result, rerender } = renderHook(({ project }) => useConnections(command, true, project), {
      initialProps: { project: "p1" },
    });
    await waitFor(() => expect(result.current.conn?.servers[1]?.project).toBe("p1"));
    rerender({ project: "p2" });
    await waitFor(() => expect(result.current.conn?.servers[1]?.project).toBe("p2"));

    expect(calls.filter(([n]) => n === "list_connections").map(([, a]) => a)).toEqual([
      { projectId: "p1" },
      { projectId: "p2" },
    ]);
    // The one everywhere is checked once; each project's linear once, as its own.
    expect(calls.filter(([n]) => n === "check_mcp_server").map(([, a]) => a)).toEqual([
      { name: "linear" },
      { name: "linear", project: "p1" },
      { name: "linear", project: "p2" },
    ]);
  });

  it("removes one project's server without touching the same name elsewhere", async () => {
    const list: Connections = {
      harnesses: [],
      servers: [server({ name: "linear", status: "connected" }), server({ name: "linear", project: "p1", status: "connected" })],
      found: [
        { name: "linear", harness: "claude", origin: ".mcp.json", project: "p1", added: true },
        { name: "linear", harness: "claude", origin: "User config", added: true },
      ],
      clis: [],
    };
    const { command } = perProject({ p1: list });
    const { result } = renderHook(() => useConnections(command, true, "p1"));
    await waitFor(() => expect(result.current.conn?.servers).toHaveLength(2));

    act(() => result.current.removeServer({ name: "linear", project: "p1" }));
    expect(result.current.conn?.servers.map((s) => s.project)).toEqual([undefined]);
    expect(result.current.conn?.found.map((f) => [f.project, f.added])).toEqual([
      ["p1", false],
      [undefined, true],
    ]);
  });
});
