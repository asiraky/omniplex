// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { render } from "~/test/harness";
import { useHoverPopover } from "./useHoverPopover";

function Hint() {
  const { open, setOpen, trigger, content } = useHoverPopover();
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger {...trigger}>trigger</PopoverTrigger>
      <PopoverContent {...content}>detail</PopoverContent>
    </Popover>
  );
}

const button = () => screen.getByRole("button", { name: "trigger" });

describe("useHoverPopover", () => {
  afterEach(() => vi.useRealTimers());

  it("opens on the first tap, though the tap focuses the trigger before it clicks", () => {
    render(<Hint />);
    fireEvent.pointerDown(button(), { pointerType: "touch" });
    // A tap's focus is not :focus-visible, which is what tells it from Tab.
    fireEvent.focus(button());
    fireEvent.click(button());
    expect(screen.getByText("detail")).toBeTruthy();

    fireEvent.pointerDown(button(), { pointerType: "touch" });
    fireEvent.click(button());
    expect(screen.queryByText("detail")).toBeNull();
  });

  it("stays open when a mouse clicks what its hover already opened", () => {
    vi.useFakeTimers();
    render(<Hint />);
    fireEvent.pointerEnter(button(), { pointerType: "mouse" });
    act(() => vi.advanceTimersByTime(200));
    expect(screen.getByText("detail")).toBeTruthy();

    fireEvent.pointerDown(button(), { pointerType: "mouse" });
    fireEvent.click(button());
    expect(screen.getByText("detail")).toBeTruthy();
  });

  it("toggles from the keyboard", () => {
    render(<Hint />);
    fireEvent.click(button());
    expect(screen.getByText("detail")).toBeTruthy();
    fireEvent.click(button());
    expect(screen.queryByText("detail")).toBeNull();
  });
});
