// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { ModelPicker } from "./ModelPicker";
import type { HarnessMeta } from "~/protocol";

// jsdom has none of the layout plumbing Radix and cmdk expect. These are the
// smallest stubs that let a popover open at all.
beforeAll(() => {
  vi.stubGlobal(
    "matchMedia",
    (query: string) => ({
      matches: true, // desktop: the popover, not the sheet
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
      onchange: null,
    }),
  );
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.scrollIntoView = () => {};
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
});

afterEach(cleanup);

const ready = { state: "ready" };

const claude: HarnessMeta = {
  id: "claude",
  name: "Claude Code",
  accent: "oklch(0.72 0.13 48)",
  permissionModes: [],
  availability: ready,
  models: [],
  instances: [
    {
      id: "claude",
      driver: "claude",
      displayName: "Claude Code",
      enabled: true,
      availability: ready,
      models: [
        {
          id: "default",
          label: "Default (recommended)",
          version: "Opus 5 with 1M context",
          description: "Best for everyday, complex tasks",
          resolves: "claude-opus-5[1m]",
          default: true,
        },
        {
          id: "sonnet",
          label: "Sonnet",
          version: "Sonnet 5",
          description: "Efficient for routine tasks",
          efforts: ["low", "medium", "high", "xhigh"],
        },
        { id: "claude-opus-4-8", label: "Opus 4.8", version: "Opus 4.8", group: "legacy" },
        { id: "claude-opus-4-7", label: "Opus 4.7", version: "Opus 4.7", group: "legacy" },
      ],
    },
  ],
} as HarnessMeta;

const codex: HarnessMeta = {
  id: "codex",
  name: "Codex",
  accent: "oklch(0.76 0.13 165)",
  permissionModes: [],
  availability: ready,
  models: [],
  instances: [
    {
      id: "codex",
      driver: "codex",
      displayName: "Codex",
      enabled: true,
      availability: ready,
      models: [{ id: "gpt-5.6-sol", label: "GPT-5.6-Sol", version: "5.6", default: true }],
    },
    {
      id: "codex_work",
      driver: "codex",
      displayName: "Codex Work",
      enabled: true,
      availability: ready,
      models: [{ id: "gpt-5.6-terra", label: "GPT-5.6-Terra", version: "5.6", default: true }],
    },
  ],
} as HarnessMeta;

/**
 * The open list. Queries run against it rather than the document, because the
 * trigger echoes the selected model and would match the same text twice.
 */
function list() {
  return within(document.querySelector("[cmdk-list]") as HTMLElement);
}

/** A row by its label; rows repeat their label as a version, so this is the
 * item, not either text node. */
function row(label: string): HTMLElement | null {
  const hits = list().queryAllByText(label);
  for (const hit of hits) {
    const item = hit.closest("[data-slot='command-item']");
    if (item) return item as HTMLElement;
  }
  return null;
}

function open(props: Partial<Parameters<typeof ModelPicker>[0]> = {}) {
  const onChange = vi.fn();
  render(
    <ModelPicker
      harnesses={[claude, codex]}
      value={{ harness: "claude", instance: "claude", model: "" }}
      onChange={onChange}
      {...props}
    />,
  );
  fireEvent.click(screen.getByRole("combobox"));
  return onChange;
}

describe("ModelPicker", () => {
  it("names the model the harness would pick, never a bare 'Default'", () => {
    render(
      <ModelPicker
        harnesses={[claude]}
        value={{ harness: "claude", instance: "claude", model: "" }}
        onChange={() => {}}
      />,
    );

    const trigger = screen.getByRole("combobox");
    expect(trigger.textContent).toContain("Default (recommended)");
    // The label alone says nothing about which model that is.
    expect(trigger.textContent).toContain("Opus 5 with 1M context");
  });

  it("shows every row as name, version, and what the model is for", () => {
    open();

    const sonnet = row("Sonnet")!;
    expect(sonnet.textContent).toContain("Sonnet 5");
    expect(sonnet.textContent).toContain("Efficient for routine tasks");
  });

  it("folds legacy models away until asked", () => {
    open();

    expect(list().getByText("Legacy (2)")).toBeTruthy();
    expect(row("Opus 4.8")).toBeNull();

    fireEvent.click(list().getByText("Legacy (2)"));
    expect(row("Opus 4.8")).toBeTruthy();
  });

  it("opens expanded when the selected model is itself legacy", () => {
    open({ value: { harness: "claude", instance: "claude", model: "claude-opus-4-7" } });

    // Hiding the current selection would be the one time the fold costs you
    // something.
    expect(row("Opus 4.7")).toBeTruthy();
  });

  it("flattens the legacy split while searching, so no match is hidden", () => {
    open();

    fireEvent.change(screen.getByPlaceholderText(/Search models/), {
      target: { value: "4.8" },
    });

    expect(list().queryByText("Legacy (2)")).toBeNull();
    expect(row("Opus 4.8")).toBeTruthy();
  });

  it("searches across accounts, not just the one being browsed", () => {
    open();

    fireEvent.change(screen.getByPlaceholderText(/Search models/), {
      target: { value: "terra" },
    });

    // Browsing Claude; the query still finds a model under a Codex account.
    expect(row("GPT-5.6-Terra")).toBeTruthy();
  });

  it("selects the harness account and the model in one interaction", () => {
    const onChange = open();

    fireEvent.click(screen.getByText("Codex Work"));
    fireEvent.click(row("GPT-5.6-Terra")!);

    expect(onChange).toHaveBeenCalledWith({
      harness: "codex",
      instance: "codex_work",
      model: "gpt-5.6-terra",
    });
  });

  it("lists two accounts of one driver separately, each with its own models", () => {
    open();

    fireEvent.click(screen.getByText("Codex Work"));
    expect(row("GPT-5.6-Terra")).toBeTruthy();
    expect(row("GPT-5.6-Sol")).toBeNull();
  });

  it("offers only models mid-session when the harness has one account", () => {
    open({ lockDriver: true, value: { harness: "claude", instance: "claude", model: "sonnet" } });

    expect(screen.queryByText("Codex Work")).toBeNull();
    expect(row("Sonnet")).toBeTruthy();
  });

  it("offers the harness's other accounts mid-session, and no other harness", () => {
    const onChange = open({
      lockDriver: true,
      value: { harness: "codex", instance: "codex", model: "gpt-5.6-sol" },
    });

    expect(screen.queryByText("Claude Code")).toBeNull();
    fireEvent.click(screen.getByText("Codex Work"));
    fireEvent.click(row("GPT-5.6-Terra")!);

    expect(onChange).toHaveBeenCalledWith({ harness: "codex", instance: "codex_work", model: "gpt-5.6-terra" });
  });

  it("leaves a disabled account out of a mid-session switch", () => {
    const disabled = {
      ...codex,
      instances: codex.instances!.map((i) => (i.id === "codex_work" ? { ...i, enabled: false } : i)),
    } as HarnessMeta;
    render(
      <ModelPicker
        harnesses={[claude, disabled]}
        value={{ harness: "codex", instance: "codex", model: "gpt-5.6-sol" }}
        onChange={() => {}}
        lockDriver
      />,
    );
    fireEvent.click(screen.getByRole("combobox"));

    expect(screen.queryByText("Codex Work")).toBeNull();
  });
});

describe("ModelPicker effort", () => {
  const effortProps = {
    lockDriver: true,
    value: { harness: "claude", instance: "claude", model: "sonnet" },
    efforts: ["low", "medium", "high", "xhigh"],
  };

  /** Opens the model menu, then the effort submenu out of it. */
  function openEfforts(props: Partial<Parameters<typeof ModelPicker>[0]> = {}) {
    const onEffortChange = vi.fn();
    render(
      <ModelPicker
        harnesses={[claude]}
        onChange={() => {}}
        onEffortChange={onEffortChange}
        {...effortProps}
        {...props}
      />,
    );
    fireEvent.click(screen.getByLabelText("Harness and model"));
    fireEvent.click(screen.getByLabelText("Reasoning effort"));
    return onEffortChange;
  }

  it("names the model and the level together, so one control says both", () => {
    render(
      <ModelPicker
        harnesses={[claude]}
        onChange={() => {}}
        onEffortChange={() => {}}
        {...effortProps}
        effort="xhigh"
      />,
    );

    const trigger = screen.getByLabelText("Harness and model");
    expect(trigger.textContent).toContain("Sonnet");
    // The harness's id is "xhigh"; nobody should have to read that.
    expect(trigger.textContent).toContain("Extra high");
    expect(trigger.textContent).not.toContain("xhigh");
  });

  it("says Auto when no level is named, rather than going blank", () => {
    render(
      <ModelPicker
        harnesses={[claude]}
        onChange={() => {}}
        onEffortChange={() => {}}
        {...effortProps}
        effort=""
      />,
    );

    expect(screen.getByLabelText("Harness and model").textContent).toContain("Auto");
  });

  it("opens the levels as a submenu of the model menu, formatted", () => {
    openEfforts({ effort: "high" });

    const menu = within(document.body);
    expect(menu.getByText("Extra high")).toBeTruthy();
    expect(menu.getByText("Medium")).toBeTruthy();
  });

  it("badges the row a session gets when no level is chosen", () => {
    openEfforts({ effort: "low" });

    const badges = document.querySelectorAll("[data-slot='badge']");
    // Exactly one: no harness reports a default level, so the only honest
    // claim is about the row that defers to it.
    expect(badges.length).toBe(1);
    expect(badges[0].textContent).toBe("Default");
    expect(badges[0].closest("[data-slot='command-item']")!.textContent).toContain("Auto");
  });

  it("names a level the model can no longer switch, rather than dropping it", () => {
    // A legacy model reports no levels; the session still ran at one, and the
    // trigger is where it is said — not in a second control beside it.
    render(
      <ModelPicker
        harnesses={[claude]}
        value={{ harness: "claude", instance: "claude", model: "claude-opus-4-7" }}
        onChange={() => {}}
        onEffortChange={() => {}}
        efforts={[]}
        effort="high"
        lockDriver
      />,
    );

    expect(screen.getByLabelText("Harness and model").textContent).toContain("High");
    fireEvent.click(screen.getByLabelText("Harness and model"));
    expect(screen.queryByLabelText("Reasoning effort")).toBeNull();
  });

  it("hands back the harness's own id, not the label shown for it", () => {
    const onEffortChange = openEfforts({ effort: "low" });

    fireEvent.click(screen.getByText("Extra high"));

    expect(onEffortChange).toHaveBeenCalledWith("xhigh");
  });

  it("offers a way back to the harness default once a level is set", () => {
    const onEffortChange = openEfforts({ effort: "high" });

    fireEvent.click(screen.getByText("Auto"));

    expect(onEffortChange).toHaveBeenCalledWith("");
  });

  it("keeps the effort row out of the way of callers that do not offer levels", () => {
    render(
      <ModelPicker
        harnesses={[claude]}
        value={{ harness: "claude", instance: "claude", model: "sonnet" }}
        onChange={() => {}}
      />,
    );
    fireEvent.click(screen.getByLabelText("Harness and model"));

    expect(screen.queryByLabelText("Reasoning effort")).toBeNull();
  });
});
