// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import type { ThreadMcpReport } from "~/protocol";

import { McpSurface } from "./McpSurface";

function setup(report: ThreadMcpReport, servers: { name: string; url?: string; command?: string }[] = []) {
  const command = vi.fn(async (name: string, args: unknown) => {
    if (name === "thread_mcp_status") return report;
    if (name === "list_connections") return { harnesses: [], servers, found: [], clis: [] };
    if (name === "thread_mcp_reconnect") {
      const target = (args as { name: string }).name;
      return {
        live: true,
        servers: report.servers.map((s) => (s.name === target ? { ...s, status: "connected" } : s)),
      };
    }
    throw new Error(`unexpected ${name}`);
  });
  const onOpenConnections = vi.fn();
  render(<McpSurface threadId="t1" command={command} onOpenConnections={onOpenConnections} />);
  return { command, onOpenConnections };
}

const flush = () => act(async () => {});

describe("McpSurface", () => {
  it("offers reconnect only on our servers the session can retry, and swaps in the answer", async () => {
    const t = setup(
      {
        live: true,
        servers: [
          { name: "ok", status: "connected" },
          { name: "broken", status: "failed", error: "boom" },
          { name: "theirs", status: "failed", error: "boom" },
        ],
      },
      [{ name: "ok", url: "https://ok.example/mcp" }, { name: "broken", url: "https://b.example/mcp" }],
    );
    await flush();
    await flush();

    const buttons = screen.getAllByRole("button", { name: "Reconnect" });
    expect(buttons).toHaveLength(1);
    fireEvent.click(buttons[0]);
    await flush();

    expect(t.command).toHaveBeenCalledWith("thread_mcp_reconnect", { threadId: "t1", name: "broken" });
    expect(screen.queryByRole("button", { name: "Reconnect" })).toBeNull();
  });

  it("links to the sign-in only for our own remote servers", async () => {
    const t = setup(
      {
        live: true,
        servers: [
          { name: "ours", status: "needs_auth" },
          { name: "theirs", status: "needs_auth" },
        ],
      },
      [{ name: "ours", url: "https://x.example/mcp" }],
    );
    await flush();
    await flush();

    const signIns = screen.getAllByRole("button", { name: "Sign in" });
    expect(signIns).toHaveLength(1);
    fireEvent.click(signIns[0]);
    expect(t.onOpenConnections).toHaveBeenCalledWith("ours");
  });

  it("shows no server rows without a running session", async () => {
    setup({ live: false, servers: [] });
    await flush();
    expect(screen.queryByRole("listitem")).toBeNull();
    expect(screen.queryByRole("button", { name: "Reconnect" })).toBeNull();
  });
});
