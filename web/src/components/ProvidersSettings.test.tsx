// @vitest-environment jsdom
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { AuthWires } from "~/components/AuthFlowDialog";
import type { HarnessMeta, ProviderInstanceMeta } from "~/protocol";
import { render } from "~/test/harness";
import ProvidersSettings from "./ProvidersSettings";

const ready = { state: "ready" as const };

function account(
  over: Partial<ProviderInstanceMeta> & { id: string },
): ProviderInstanceMeta {
  return {
    driver: "claude",
    displayName: over.id,
    enabled: true,
    availability: ready,
    ...over,
  };
}

function harness(
  over: Partial<HarnessMeta> & { id: string; name: string },
): HarnessMeta {
  return {
    accent: "",
    models: [],
    permissionModes: [],
    availability: ready,
    instances: [],
    ...over,
  };
}

function open(
  harnesses: HarnessMeta[],
  command: AuthWires["command"] = vi.fn(async () => ({})),
) {
  const wires: AuthWires = { command, subscribe: () => () => {} };
  render(
    <ProvidersSettings
      harnesses={harnesses}
      wires={wires}
      onOpenTerminal={vi.fn()}
      onRecheck={vi.fn()}
    />,
  );
  return { command };
}

const conflicted = harness({
  id: "claude",
  name: "Claude Code",
  instances: [
    account({
      id: "zero8",
      displayName: "zero8",
      homeConflicts: [
        {
          entry: "skills",
          path: "/h/.omniplex/instances/zero8/skills",
          shared: "/h/.claude/skills",
        },
        {
          entry: "CLAUDE.md",
          path: "/h/.omniplex/instances/zero8/CLAUDE.md",
          shared: "/h/.claude/CLAUDE.md",
        },
      ],
    }),
  ],
});

describe("an account's own copy of shared config", () => {
  it("names each entry and settles one with the shared copy", async () => {
    const { command } = open([conflicted]);
    const warning = screen.getByRole("alert");
    expect(warning.textContent).toMatch(/skills and CLAUDE\.md/);

    await act(async () => {
      fireEvent.click(
        within(warning).getByRole("button", { name: "Use shared CLAUDE.md" }),
      );
    });

    expect(command).toHaveBeenCalledWith("use_shared_home_entry", {
      instanceId: "zero8",
      entry: "CLAUDE.md",
    });
  });

  it("holds every entry's button while one is being settled", async () => {
    let finish: (v: unknown) => void = () => {};
    open(
      [conflicted],
      vi.fn(() => new Promise((r) => (finish = r))),
    );

    fireEvent.click(screen.getByRole("button", { name: "Use shared skills" }));

    const other = screen.getByRole("button", {
      name: "Use shared CLAUDE.md",
    }) as HTMLButtonElement;
    expect(other.disabled).toBe(true);
    await act(async () => finish({ status: "shared" }));
    expect(other.disabled).toBe(false);
  });

  it("shows why it could not be settled", async () => {
    open(
      [conflicted],
      vi.fn(async () => {
        throw new Error("backup exists");
      }),
    );
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Use shared skills" }),
      );
    });
    await waitFor(() => expect(screen.getByText("backup exists")).toBeTruthy());
  });

  it("offers the same fix on the account's own screen", async () => {
    const { command } = open([conflicted]);
    fireEvent.click(screen.getByRole("button", { name: /^zero8/ }));

    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Use shared skills" }),
      );
    });
    expect(command).toHaveBeenCalledWith("use_shared_home_entry", {
      instanceId: "zero8",
      entry: "skills",
    });
  });
});

describe("who each account is signed in as", () => {
  const twins = harness({
    id: "claude",
    name: "Claude Code",
    instances: [
      account({
        id: "claude",
        displayName: "Aaron",
        availability: { state: "ready", facts: { account: "aaron@zero8.ai" } },
        sameLoginAs: ["zero8"],
      }),
      account({
        id: "zero8",
        displayName: "zero8",
        availability: { state: "ready", facts: { account: "aaron@zero8.ai" } },
        sameLoginAs: ["claude"],
      }),
      account({
        id: "aaron",
        displayName: "Worksauce",
        availability: {
          state: "unavailable",
          reason: "Claude is not signed in.",
          remedy: [{ text: "Sign in", action: "login" }],
        },
      }),
    ],
  });

  it("shows the email, or that the account is signed out", () => {
    open([twins]);
    expect(screen.getByRole("button", { name: /^Aaron/ }).textContent).toMatch(
      /aaron@zero8\.ai/,
    );
    // Said once: the harness's own words, not a second line repeating them.
    const out =
      screen.getByRole("button", { name: /^Worksauce/ }).textContent ?? "";
    expect(out.match(/not signed in/gi)).toHaveLength(1);
  });

  it("warns that two accounts are the same login, naming the other", () => {
    open([twins]);
    const warnings = screen.getAllByRole("alert").map((a) => a.textContent);
    expect(warnings).toHaveLength(2);
    expect(warnings[0]).toMatch(/same login as zero8/);
    expect(warnings[1]).toMatch(/same login as Aaron/);
  });

  it("warns on the account's own screen too", () => {
    open([twins]);
    fireEvent.click(screen.getByRole("button", { name: /^zero8/ }));
    expect(screen.getByRole("alert").textContent).toMatch(
      /same login as Aaron/,
    );
    expect(screen.getByText("aaron@zero8.ai")).toBeTruthy();
  });
});

describe("an account still on the machine's own home", () => {
  const ambient = harness({
    id: "claude",
    name: "Claude Code",
    instances: [
      account({ id: "claude", displayName: "Aaron", machineHome: true }),
      account({ id: "zero8" }),
    ],
  });

  it("is warned about in the list, and only that account", () => {
    open([ambient]);
    const warnings = screen.getAllByRole("alert");
    expect(warnings).toHaveLength(1);
    expect(warnings[0].textContent).toMatch(/migrate-homes/);
    expect(warnings[0].parentElement?.parentElement?.textContent).toMatch(
      /Aaron/,
    );
  });

  it("is warned about on the account's own screen", () => {
    open([ambient]);
    fireEvent.click(screen.getByRole("button", { name: /^Aaron/ }));
    expect(screen.getByRole("alert").textContent).toMatch(/migrate-homes/);
  });
});

describe("a driver with no account", () => {
  it("adds one from its own group, straight into that driver's form", () => {
    open([
      harness({
        id: "claude",
        name: "Claude Code",
        instances: [account({ id: "claude" })],
      }),
      harness({ id: "codex", name: "Codex" }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Add Codex account" }));
    expect(screen.getByText("New Codex account")).toBeTruthy();
  });
});

describe("saving and removing", () => {
  it("saves an existing account rather than adding it", async () => {
    const { command } = open([
      harness({
        id: "claude",
        name: "Claude Code",
        instances: [account({ id: "claude" })],
      }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: /^claude/ }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Save" }));
    });
    expect(command).toHaveBeenCalledWith(
      "save_provider_instance",
      expect.anything(),
    );
  });

  it("removes an account whose id is its driver's", async () => {
    const { command } = open([
      harness({
        id: "claude",
        name: "Claude Code",
        instances: [account({ id: "claude" })],
      }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: /^claude/ }));
    fireEvent.click(screen.getByRole("button", { name: "Remove account" }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    });
    expect(command).toHaveBeenCalledWith("delete_provider_instance", {
      instanceId: "claude",
    });
  });
});
