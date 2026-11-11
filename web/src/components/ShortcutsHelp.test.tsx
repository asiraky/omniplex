// @vitest-environment jsdom
import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { COMMANDS } from "~/lib/keybindings";
import { render, viewport } from "~/test/harness";
import { ShortcutsHelp } from "./ShortcutsHelp";

afterEach(() => {
  delete window.omniplexDesktop;
  vi.unstubAllGlobals();
});

/** The desktop app on `platform`, or a browser tab on a Mac or Linux. */
function shell(where: "app" | "browser", platform: "darwin" | "linux") {
  if (where === "app") window.omniplexDesktop = { platform };
  else vi.stubGlobal("navigator", { platform: platform === "darwin" ? "MacIntel" : "Linux x86_64" });
}

/** The chips on a row, as the reader sees them. */
function chips(label: string) {
  const row = screen.getAllByRole("listitem").find((li) => li.firstElementChild?.textContent === label);
  if (!row) throw new Error(`no row "${label}"`);
  return [...row.querySelectorAll("kbd")].map((k) => k.textContent);
}

const renderHelp = () => render(<ShortcutsHelp open onOpenChange={() => {}} />);

describe("ShortcutsHelp", () => {
  it("lists the commands under their groups", () => {
    shell("browser", "darwin");
    renderHelp();

    const section = (group: string) => screen.getByRole("heading", { name: group }).closest("section")!;
    for (const id of ["toggleSidebar", "settings", "modelPicker"] as const) {
      const { group, label } = COMMANDS[id];
      expect(within(section(group)).getByText(label)).toBeTruthy();
    }
  });

  it("leaves out in a browser what only the desktop app can take", () => {
    shell("browser", "darwin");
    renderHelp();

    expect(screen.queryByText("Close side panel")).toBeNull();
    expect(screen.queryByText("Go to thread 1–9")).toBeNull();
    // The browser keeps ⌘N, so the browser chord is the one shown.
    expect(chips("New thread in…")).toEqual(["⇧", "⌘", "O"]);
  });

  it("shows the jump row and the app's own chords in the desktop app", () => {
    shell("app", "darwin");
    renderHelp();

    expect(chips("Go to thread 1–9")).toEqual(["⌘", "1", "⌘", "9"]);
    expect(chips("Close side panel")).toEqual(["⌘", "W"]);
    expect(chips("New thread in…")).toEqual(["⌘", "N"]);
    // One row for the nine, not nine rows.
    expect(screen.queryByText("Go to thread 1")).toBeNull();
  });

  it("shows every chord a command has", () => {
    shell("browser", "darwin");
    renderHelp();

    expect(chips("Keyboard shortcuts")).toEqual(["⌘", "/", "?"]);
    // Escape is its own command, so it can wait for the composer's menus, but
    // reads as one more way to stop.
    expect(chips("Stop the turn")).toEqual(["⌘", ".", "Esc"]);
    expect(screen.getAllByText("Stop the turn")).toHaveLength(1);
  });

  it("writes chords for the platform", () => {
    shell("app", "linux");
    renderHelp();

    expect(chips("Model picker")).toEqual(["Ctrl", "Shift", "M"]);
    expect(chips("Go to thread 1–9")).toEqual(["Ctrl", "1", "Ctrl", "9"]);
    expect(screen.getByText(/Also in the palette/).textContent).toBe("Also in the palette: Ctrl+K");
  });

  it("points at the palette with the Mac chord on a Mac", () => {
    shell("browser", "darwin");
    renderHelp();

    expect(screen.getByText(/Also in the palette/).textContent).toBe("Also in the palette: ⌘K");
  });

  it("lists everything on a phone too", () => {
    viewport("phone");
    shell("browser", "darwin");
    renderHelp();

    expect(screen.getByText("Toggle threads")).toBeTruthy();
    expect(screen.getByRole("dialog").hasAttribute("data-fullscreen-mobile")).toBe(true);
  });

  it("renders nothing while closed", () => {
    render(<ShortcutsHelp open={false} onOpenChange={() => {}} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
