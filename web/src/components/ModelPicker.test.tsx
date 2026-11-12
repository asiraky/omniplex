// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
          label: "Opus 5",
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
  it("names the preselected model and adds nothing when it has no version", () => {
    render(
      <ModelPicker
        harnesses={[claude]}
        value={{ harness: "claude", instance: "claude", model: "" }}
        onChange={() => {}}
      />,
    );

    const trigger = screen.getByRole("combobox");
    // The logo's title comes first; nothing may follow the label.
    expect(trigger.textContent?.trim()).toMatch(/Opus 5$/);
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

  it("starts over at the selection with no query each time it opens", () => {
    open();
    fireEvent.click(screen.getByText("Codex Work"));
    fireEvent.change(screen.getByPlaceholderText(/Search models/), {
      target: { value: "terra" },
    });

    fireEvent.keyDown(screen.getByPlaceholderText(/Search models/), { key: "Escape" });
    expect(screen.queryByPlaceholderText(/Search models/)).toBeNull();
    fireEvent.click(screen.getByRole("combobox"));

    expect((screen.getByPlaceholderText(/Search models/) as HTMLInputElement).value).toBe("");
    // Back on the selected account's models, not the one browsed last time.
    expect(row("Sonnet")).toBeTruthy();
    expect(row("GPT-5.6-Terra")).toBeNull();
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

  it("offers only models mid-thread when the harness has one account", () => {
    open({ lockDriver: true, value: { harness: "claude", instance: "claude", model: "sonnet" } });

    expect(screen.queryByText("Codex Work")).toBeNull();
    expect(row("Sonnet")).toBeTruthy();
  });

  it("offers the harness's other accounts mid-thread, and no other harness", () => {
    const onChange = open({
      lockDriver: true,
      value: { harness: "codex", instance: "codex", model: "gpt-5.6-sol" },
    });

    expect(screen.queryByText("Claude Code")).toBeNull();
    fireEvent.click(screen.getByText("Codex Work"));
    fireEvent.click(row("GPT-5.6-Terra")!);

    expect(onChange).toHaveBeenCalledWith({ harness: "codex", instance: "codex_work", model: "gpt-5.6-terra" });
  });

  it("leaves a disabled account out of a mid-thread switch", () => {
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

  /** Opens the model menu, whose foot carries the levels. */
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
    return onEffortChange;
  }

  const levels = () => within(screen.getByRole("radiogroup", { name: "Reasoning effort" }));
  const isOpen = () => !!screen.queryByPlaceholderText(/Search models/);

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

  it("offers Auto and each of the model's levels, with the one in effect checked", () => {
    openEfforts({ effort: "high" });

    const radios = levels().getAllByRole("radio");
    expect(radios.map((r) => r.textContent)).toEqual(["Auto", "Low", "Medium", "High", "Extra high"]);
    expect(radios.filter((r) => r.getAttribute("aria-checked") === "true")).toEqual([
      levels().getByRole("radio", { name: "High" }),
    ]);
  });

  it("names a level the model can no longer switch, rather than dropping it", () => {
    // A legacy model reports no levels; the thread still ran at one, and the
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
    expect(isOpen()).toBe(true);
    expect(screen.queryByRole("radiogroup", { name: "Reasoning effort" })).toBeNull();
  });

  it("hands back the harness's own id, not the label shown for it, and closes", () => {
    const onEffortChange = openEfforts({ effort: "low" });

    fireEvent.click(levels().getByRole("radio", { name: "Extra high" }));

    expect(onEffortChange).toHaveBeenCalledWith("xhigh");
    // The effort is the last choice the menu offers.
    expect(isOpen()).toBe(false);
  });

  it("offers a way back to the harness default once a level is set", () => {
    const onEffortChange = openEfforts({ effort: "high" });

    fireEvent.click(levels().getByRole("radio", { name: "Auto" }));

    expect(onEffortChange).toHaveBeenCalledWith("");
  });

  it("closes on the level already in effect without asking for it again", () => {
    const onEffortChange = openEfforts({ effort: "high" });

    fireEvent.click(levels().getByRole("radio", { name: "High" }));

    expect(onEffortChange).not.toHaveBeenCalled();
    expect(isOpen()).toBe(false);
  });

  it("stays open on a model pick that leaves a level to choose", () => {
    const onChange = vi.fn();
    openEfforts({ onChange, lockDriver: false, value: { harness: "claude", instance: "claude", model: "" } });

    fireEvent.click(row("Sonnet")!);

    expect(onChange).toHaveBeenCalledWith({ harness: "claude", instance: "claude", model: "sonnet" });
    expect(isOpen()).toBe(true);
  });

  it("closes on a model pick when that model has no levels to choose", () => {
    const onChange = vi.fn();
    openEfforts({ onChange });

    fireEvent.click(row("Opus 5")!);

    expect(onChange).toHaveBeenCalled();
    expect(isOpen()).toBe(false);
  });

  it("moves between levels with the arrow keys without choosing one", () => {
    const onEffortChange = openEfforts({ effort: "high" });
    const high = levels().getByRole("radio", { name: "High" });
    high.focus();

    fireEvent.keyDown(high, { key: "ArrowRight" });
    expect(document.activeElement).toBe(levels().getByRole("radio", { name: "Extra high" }));
    // Past the end wraps to the start.
    fireEvent.keyDown(document.activeElement!, { key: "ArrowRight" });
    expect(document.activeElement).toBe(levels().getByRole("radio", { name: "Auto" }));
    fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" });
    expect(document.activeElement).toBe(levels().getByRole("radio", { name: "Extra high" }));

    expect(onEffortChange).not.toHaveBeenCalled();
    expect(isOpen()).toBe(true);
  });

  it("opens with focus on the level in effect when asked for effort", async () => {
    const props = {
      harnesses: [claude],
      onChange: () => {},
      onEffortChange: () => {},
      ...effortProps,
      effort: "medium",
      onOpenChange: () => {},
    };
    const { rerender } = render(<ModelPicker {...props} open={false} />);

    rerender(<ModelPicker {...props} open focusRequest={{ target: "effort" }} />);

    // Not the search, which is where opening would otherwise put it.
    await waitFor(() =>
      expect(document.activeElement).toBe(levels().getByRole("radio", { name: "Medium" })),
    );
  });

  it("moves focus to the level, and back to the search, while already open", async () => {
    const props = {
      harnesses: [claude],
      onChange: () => {},
      onEffortChange: () => {},
      ...effortProps,
      effort: "low",
      open: true,
      onOpenChange: () => {},
    };
    const { rerender } = render(<ModelPicker {...props} />);
    const search = screen.getByPlaceholderText(/Search models/);
    await waitFor(() => expect(document.activeElement).toBe(search));

    rerender(<ModelPicker {...props} focusRequest={{ target: "effort" }} />);
    expect(document.activeElement).toBe(levels().getByRole("radio", { name: "Low" }));

    rerender(<ModelPicker {...props} focusRequest={{ target: "model" }} />);
    expect(document.activeElement).toBe(search);
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

    expect(screen.queryByRole("radiogroup", { name: "Reasoning effort" })).toBeNull();
  });
});
