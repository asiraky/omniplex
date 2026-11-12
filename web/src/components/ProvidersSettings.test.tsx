// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import type { AuthWires } from "./AuthFlowDialog";
import ProvidersSettings from "./ProvidersSettings";
import type { HarnessMeta, HarnessUpdate, ProviderInstanceMeta } from "~/protocol";

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
    expect(screen.getByText(/Auto-update failed.*EACCES/)).toBeTruthy();
  });

  it("leaves the row unmarked when the last update succeeded", () => {
    open({ at: Date.now() - 60_000, from: "2.1.252", to: "2.1.300" });
    expect(screen.queryByText("update failed")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Claude Code/ }));
    expect(screen.getByText(/2\.1\.252 → 2\.1\.300/)).toBeTruthy();
  });

  it("shows only the CLI version when no update has been reported", () => {
    open();
    expect(screen.queryByText("update failed")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Claude Code/ }));
    expect(screen.getByText("Claude Code 2.1.300")).toBeTruthy();
    expect(screen.queryByText(/Auto-update|Up to date|Updated/)).toBeNull();
  });
});

function claudeAccount(over: Partial<ProviderInstanceMeta> = {}): ProviderInstanceMeta {
  return {
    id: "claude",
    driver: "claude",
    displayName: "Aaron",
    enabled: true,
    availability: { state: "ready" },
    ...over,
  };
}

function setup(harnesses: HarnessMeta[], extra: { addFor?: string | null; command?: AuthWires["command"] } = {}) {
  const command: AuthWires["command"] = extra.command ?? vi.fn().mockResolvedValue({});
  render(
    <ProvidersSettings
      harnesses={harnesses}
      wires={{ command, subscribe: () => () => {} }}
      onOpenTerminal={() => {}}
      onRecheck={() => {}}
      addFor={extra.addFor}
    />,
  );
  return { command };
}

const codex: HarnessMeta = {
  id: "codex",
  name: "Codex",
  accent: "#000",
  models: [],
  permissionModes: [],
  availability: {
    state: "unavailable",
    reason: "No Codex account yet.",
    remedy: [{ text: "Add account", action: "add_account" }],
  },
  instances: [],
};

const withAccounts = (...instances: ProviderInstanceMeta[]): HarnessMeta => ({
  ...harness(),
  instances,
});

describe("ProvidersSettings accounts", () => {
  it("offers Add account in a driver's own group when it has none, preselecting that driver", () => {
    setup([withAccounts(claudeAccount()), codex]);
    expect(within(screen.getByRole("group", { name: "Claude Code" })).queryByRole("button", { name: "Add account" })).toBeNull();

    fireEvent.click(within(screen.getByRole("group", { name: "Codex" })).getByRole("button", { name: "Add account" }));

    expect(screen.getByText("New Codex account")).toBeTruthy();
  });

  it("opens on the add view for the driver it was asked to add to", () => {
    setup([withAccounts(claudeAccount()), codex], { addFor: "codex" });
    expect(screen.getByText("New Codex account")).toBeTruthy();
  });

  it("asks which agent when asked to add with none preselected", () => {
    setup([withAccounts(claudeAccount()), codex], { addFor: null });
    expect(screen.getByText("Which agent?")).toBeTruthy();
  });

  it("shows the signed-in email on the row and the account screen", () => {
    setup([withAccounts(claudeAccount({ account: "aaron@example.com" }))]);
    expect(screen.getByText(/aaron@example\.com/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /Aaron/ }));
    expect(screen.getByText("aaron@example.com")).toBeTruthy();
  });

  it("warns, naming the other account, when two accounts share a login", () => {
    setup([
      withAccounts(
        claudeAccount({ sameLoginAs: ["zero8"] }),
        claudeAccount({ id: "zero8", displayName: "zero8", sameLoginAs: ["claude"] }),
      ),
    ]);
    expect(screen.getByText("Same login as zero8")).toBeTruthy();
    expect(screen.getByText("Same login as Aaron")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Aaron/ }));
    expect(screen.getByText(/Signed in to the same login as zero8/)).toBeTruthy();
  });

  it("shows no login warning when each account is its own login", () => {
    setup([withAccounts(claudeAccount(), claudeAccount({ id: "zero8", displayName: "zero8" }))]);
    expect(screen.queryByText(/Same login/)).toBeNull();
  });

  it("moves an account's own copy aside with Use shared and says where it went", async () => {
    const command = vi.fn().mockResolvedValue({ backup: "/home/a/.omniplex/instances/claude/skills.account-backup" });
    setup([withAccounts(claudeAccount({ configConflicts: ["skills", "CLAUDE.md"] }))], { command });
    expect(screen.getByText(/Own copy of skills and CLAUDE\.md/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Aaron/ }));

    fireEvent.click(screen.getByRole("button", { name: "Use shared skills" }));

    expect(command).toHaveBeenCalledWith("use_shared_config", { instanceId: "claude", entry: "skills" });
    expect(await screen.findByText("/home/a/.omniplex/instances/claude/skills.account-backup")).toBeTruthy();
    // The other entry is still waiting on its own button.
    expect(screen.getByRole("button", { name: "Use shared CLAUDE.md" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Use shared skills" })).toBeNull();
  });

  it("keeps the entry and shows the error when Use shared fails", async () => {
    const command = vi.fn().mockRejectedValue(new Error("destination exists"));
    setup([withAccounts(claudeAccount({ configConflicts: ["skills"] }))], { command });
    fireEvent.click(screen.getByRole("button", { name: /Aaron/ }));

    fireEvent.click(screen.getByRole("button", { name: "Use shared skills" }));

    expect(await screen.findByText("destination exists")).toBeTruthy();
    await waitFor(() =>
      expect((screen.getByRole("button", { name: "Use shared skills" }) as HTMLButtonElement).disabled).toBe(false),
    );
  });

  it("saves and removes any account, the first one included", async () => {
    const { command } = setup([withAccounts(claudeAccount())]);
    fireEvent.click(screen.getByRole("button", { name: /Aaron/ }));

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("save_provider_instance", expect.objectContaining({ spec: expect.objectContaining({ id: "claude" }) })),
    );

    fireEvent.click(screen.getByRole("button", { name: "Remove account" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(command).toHaveBeenCalledWith("delete_provider_instance", { instanceId: "claude" }));
  });
});
