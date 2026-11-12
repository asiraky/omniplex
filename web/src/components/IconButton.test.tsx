// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { IconButton } from "./IconButton";
import { render } from "~/test/harness";

/** A desktop, with or without a mouse; the width query always passes. */
function pointer(fine: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: /min-width/.test(query) || (/hover: hover/.test(query) && fine),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

const tooltipFor = () => {
  fireEvent.focus(screen.getByRole("button", { name: "Hide threads" }));
  return screen.getByRole("tooltip").textContent;
};

afterEach(() => vi.unstubAllGlobals());

describe("IconButton", () => {
  it("follows the label with the chord where shortcuts are advertised", () => {
    pointer(true);
    vi.stubGlobal("omniplexDesktop", { platform: "darwin" });
    render(
      <IconButton label="Hide threads" shortcut="toggleSidebar">
        <span />
      </IconButton>,
    );
    expect(tooltipFor()).toBe("Hide threads⌘B");
  });

  it("says the label alone without a mouse to hover with", () => {
    pointer(false);
    render(
      <IconButton label="Hide threads" shortcut="toggleSidebar">
        <span />
      </IconButton>,
    );
    expect(tooltipFor()).toBe("Hide threads");
  });

  it("says the label alone for a command with no chord in this shell", () => {
    // Jumping to a thread is the desktop app's alone; a browser tab owns ⌘1.
    pointer(true);
    render(
      <IconButton label="Hide threads" shortcut="jumpThread1">
        <span />
      </IconButton>,
    );
    expect(tooltipFor()).toBe("Hide threads");
  });
});
