// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { modelList } from "~/components/ModelPicker";
import { ComposerModelPicker } from "./ComposerModelPicker";
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

// The list arrives as its own chunk; in, it renders on the open itself.
beforeAll(async () => {
  await modelList.load();
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
        { id: "opus", label: "Opus", resolves: "claude-opus-5" },
        { id: "sonnet", label: "Sonnet" },
        { id: "haiku", label: "Haiku" },
      ],
    },
  ],
} as HarnessMeta;

/** The open list; the trigger echoes the running model's label too. */
function row(label: string): HTMLElement {
  const list = within(document.querySelector("[cmdk-list]") as HTMLElement);
  return list.getByText(label).closest("[data-slot='command-item']") as HTMLElement;
}

function Picker({
  onSwitchModel,
  // What the harness reported, not the alias the row carries.
  model = "claude-opus-5",
}: {
  onSwitchModel: (id: string) => void;
  model?: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <ComposerModelPicker
      harnesses={[claude]}
      harness="claude"
      instance="claude"
      model={model}
      effort=""
      serviceTier=""
      contextWindow={undefined}
      anyHarness={false}
      disabled={false}
      open={open}
      onOpenChange={setOpen}
      onSwitchModel={onSwitchModel}
    />
  );
}

function openPicker() {
  const onSwitchModel = vi.fn();
  const { rerender } = render(<Picker onSwitchModel={onSwitchModel} />);
  fireEvent.click(screen.getByRole("combobox"));
  return { onSwitchModel, rerender };
}

/** A model with no efforts closes the menu on a pick. */
function reopen() {
  fireEvent.click(screen.getByRole("combobox"));
}

describe("ComposerModelPicker", () => {
  it("sends no switch for the row the thread is already running", () => {
    const { onSwitchModel } = openPicker();

    fireEvent.click(row("Opus"));

    expect(onSwitchModel).not.toHaveBeenCalled();
  });

  it("switches to a different row by its own id", () => {
    const { onSwitchModel } = openPicker();

    fireEvent.click(row("Sonnet"));

    expect(onSwitchModel).toHaveBeenCalledTimes(1);
    expect(onSwitchModel).toHaveBeenCalledWith("sonnet");
  });

  it("sends the running row again to undo a switch still in flight", () => {
    const { onSwitchModel } = openPicker();

    fireEvent.click(row("Sonnet"));
    reopen();
    fireEvent.click(row("Opus"));

    expect(onSwitchModel.mock.calls).toEqual([["sonnet"], ["opus"]]);
  });

  it("keeps waiting on the last pick when an earlier one is echoed first", () => {
    const { onSwitchModel, rerender } = openPicker();

    fireEvent.click(row("Sonnet"));
    reopen();
    fireEvent.click(row("Haiku"));
    // Sonnet lands; Haiku is still on its way.
    rerender(<Picker onSwitchModel={onSwitchModel} model="sonnet" />);
    reopen();
    fireEvent.click(row("Sonnet"));

    expect(onSwitchModel.mock.calls).toEqual([["sonnet"], ["haiku"], ["sonnet"]]);
  });
});
