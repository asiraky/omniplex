// @vitest-environment jsdom
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { render } from "~/test/harness";
import { ThemeToggle } from "./ThemeToggle";

const openMenu = () =>
  fireEvent.pointerDown(screen.getByRole("button", { name: /^Theme:/ }), {
    button: 0,
    ctrlKey: false,
  });

describe("tint", () => {
  beforeEach(() => {
    localStorage.clear();
    delete document.documentElement.dataset.tint;
  });

  it("applies the picked tint, keeps the menu open for the next, and remembers it", async () => {
    render(<ThemeToggle />);
    openMenu();
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "Green" }));
    expect(document.documentElement.dataset.tint).toBe("green");
    fireEvent.click(screen.getByRole("menuitemradio", { name: "Violet" }));
    expect(document.documentElement.dataset.tint).toBe("violet");

    cleanup();
    delete document.documentElement.dataset.tint;
    render(<ThemeToggle />);
    expect(document.documentElement.dataset.tint).toBe("violet");
  });

  it("clears the attribute for the default and ignores a tint it does not know", () => {
    localStorage.setItem("omniplex.tint", "chartreuse");
    document.documentElement.dataset.tint = "chartreuse";
    render(<ThemeToggle />);
    expect(document.documentElement.dataset.tint).toBeUndefined();
  });
});
