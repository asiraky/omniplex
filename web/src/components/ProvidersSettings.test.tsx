// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import ProvidersSettings from "./ProvidersSettings";
import type { HarnessMeta, HarnessUpdate } from "~/protocol";

function harness(update?: HarnessUpdate): HarnessMeta {
  return {
    id: "claude",
    name: "Claude Code",
    accent: "#000",
    models: [],
    permissionModes: [],
    availability: { state: "ready" },
    update,
    instances: [
      {
        id: "claude",
        driver: "claude",
        displayName: "Claude Code",
        enabled: true,
        availability: { state: "ready", facts: { version: "2.1.300" } },
      },
    ],
  };
}

function open(update?: HarnessUpdate) {
  render(
    <ProvidersSettings
      harnesses={[harness(update)]}
      wires={{ command: vi.fn(), subscribe: () => () => {} }}
      onOpenTerminal={() => {}}
      onRecheck={() => {}}
    />,
  );
}

describe("ProvidersSettings harness CLI updates", () => {
  it("marks a failed auto-update on the account row and shows the error inside", () => {
    open({ at: Date.now() - 60_000, from: "2.1.300", to: "2.1.300", error: "EACCES" });
    expect(screen.getByText("update failed")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Claude Code/ }));
    expect(screen.getByText(/EACCES/).className).toContain("text-attention-foreground");
  });

  it("leaves the row unmarked when the last update succeeded", () => {
    open({ at: Date.now() - 60_000, from: "2.1.252", to: "2.1.300" });
    expect(screen.queryByText("update failed")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Claude Code/ }));
    expect(screen.getByText(/2\.1\.252 → 2\.1\.300/).className).not.toContain("text-attention-foreground");
  });
});
