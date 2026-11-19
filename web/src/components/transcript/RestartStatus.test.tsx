// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentNavContext } from "~/lib/agentNav";
import type { Agent } from "~/protocol";

import { RestartStatus } from "./RestartStatus";

afterEach(cleanup);

describe("RestartStatus", () => {
  it("takes back a restart waiting on the turn", () => {
    const onCancel = vi.fn();
    render(<RestartStatus pending restarting={false} onCancel={onCancel} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel restart" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("offers nothing to cancel while a restart is under way", () => {
    render(<RestartStatus pending={false} restarting onCancel={() => {}} />);
    expect(screen.getByRole("status")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("draws nothing when no restart is owed", () => {
    const { container } = render(<RestartStatus pending={false} restarting={false} onCancel={() => {}} />);
    expect(container.textContent).toBe("");
  });

  it("names the saved prompt an agent's restart is waiting to bring in", () => {
    const status = (live: number) => {
      cleanup();
      render(
        <AgentNavContext.Provider
          value={{ current: { promptRev: 8, livePromptRev: live } as Agent, openAgent: () => {}, openThread: () => {} }}
        >
          <RestartStatus pending restarting={false} />
        </AgentNavContext.Provider>,
      );
      return screen.getByRole("status").textContent;
    };
    expect(status(7)).toMatch(/\br8\b/);
    expect(status(8)).not.toMatch(/\br\d+\b/);
  });
});
