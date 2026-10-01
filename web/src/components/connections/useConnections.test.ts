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
    oauth: false,
    status: "unchecked",
    accounts: [],
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
      result.current.removeServer("a");
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
});
