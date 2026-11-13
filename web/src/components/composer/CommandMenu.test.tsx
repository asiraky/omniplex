// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import type { ComposerItem } from "~/protocol";

import { CommandMenu } from "./CommandMenu";

/** The rows' import, held until the test lets it through. */
const gate = vi.hoisted(() => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { held, release: () => release() };
});

vi.mock("./CommandMenuList", async (importOriginal) => {
  await gate.held;
  return importOriginal();
});

// Radix measures its anchor and cmdk scrolls the active row into view; jsdom
// does neither.
beforeAll(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.scrollIntoView = () => {};
});

afterEach(cleanup);

const item = (name: string): ComposerItem => ({
  id: `command:${name}`,
  name,
  kind: "command",
  trigger: "/",
  insertText: `/${name}`,
  behavior: "prompt",
});

function Menu({ matches }: { matches: ComposerItem[] }) {
  const ref = useRef<HTMLTextAreaElement>(null);
  return (
    <CommandMenu
      open
      anchor={<textarea ref={ref} aria-label="Message" />}
      anchorRef={ref}
      onDismiss={() => {}}
      matches={matches}
      activeIndex={0}
      loading={false}
      onHover={() => {}}
      onChoose={() => {}}
    />
  );
}

describe("CommandMenu", () => {
  it("holds the open menu's place with a spinner until its rows arrive", async () => {
    render(<Menu matches={[item("compact"), item("review")]} />);
    expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
    expect(screen.queryByRole("option")).toBeNull();
    expect(screen.queryByText("/compact")).toBeNull();

    await act(async () => gate.release());
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(2));
    expect(screen.getByText("/compact")).toBeTruthy();
    expect(screen.getByText("/review")).toBeTruthy();
    expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
  });
});
