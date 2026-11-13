// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { ModelPicker } from "./ModelPicker";
import type { HarnessMeta } from "~/protocol";

/**
 * The list's import, held until the test settles it. Each import of the
 * module takes the gate set at that moment, so a rejected one can be followed
 * by a held one that succeeds.
 */
const gate = vi.hoisted(() => {
  let settle: { resolve: () => void; reject: (e: Error) => void } | undefined;
  let held = Promise.resolve();
  return {
    hold() {
      held = new Promise<void>((resolve, reject) => {
        settle = { resolve, reject };
      });
    },
    wait: () => held,
    resolve: () => settle!.resolve(),
    reject: (e: Error) => settle!.reject(e),
  };
});

vi.mock("./ModelList", async (importOriginal) => {
  await gate.wait();
  return importOriginal();
});

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
        { id: "default", label: "Opus 5", default: true },
        { id: "sonnet", label: "Sonnet", version: "Sonnet 5" },
      ],
    },
  ],
} as HarnessMeta;

function open() {
  render(
    <ModelPicker
      harnesses={[claude]}
      value={{ harness: "claude", instance: "claude", model: "" }}
      onChange={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("combobox"));
}

const search = () => screen.getByPlaceholderText("Search models…") as HTMLInputElement;
const rows = () => document.querySelectorAll("[data-slot='command-item']");

// The chunk is module-level and keeps what arrives, so these run in order: a
// failure first, then the hold that finally lets the list in.
describe("ModelPicker before its list has arrived", () => {
  it("offers a reload when the list could not be fetched", async () => {
    gate.hold();
    open();
    await act(async () => gate.reject(new Error("offline")));
    expect(await screen.findByRole("button", { name: "Reload" })).toBeTruthy();
    expect(rows()).toHaveLength(0);
  });

  it("takes the typing in a stand-in and hands it, focused, to the real search", async () => {
    gate.hold();
    open();
    expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
    expect(document.querySelector("[cmdk-input]")).toBeNull();
    expect(rows()).toHaveLength(0);

    const standIn = search();
    standIn.focus();
    fireEvent.change(standIn, { target: { value: "son" } });

    await act(async () => gate.resolve());
    const real = await waitFor(() => {
      const el = document.querySelector<HTMLInputElement>("[cmdk-input]");
      expect(el).toBeTruthy();
      return el!;
    });
    expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
    expect(real.value).toBe("son");
    expect(document.activeElement).toBe(real);
    expect(real.selectionStart).toBe(3);
    expect(real.selectionEnd).toBe(3);
    expect(screen.getByText("Sonnet").closest("[data-slot='command-item']")).toBeTruthy();
  });
});
