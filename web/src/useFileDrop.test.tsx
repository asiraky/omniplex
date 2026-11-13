// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import type { DragEvent } from "react";
import { describe, expect, it, vi } from "vitest";

import { useFileDrop } from "./useFileDrop";

const file = new File(["x"], "x.png", { type: "image/png" });

/** A drag event as the handlers read it: what it carries, and whether it was taken. */
const drag = (types: string[], files: File[] = []) => {
  const preventDefault = vi.fn();
  const event = { dataTransfer: { types, files }, preventDefault } as unknown as DragEvent;
  return { event, preventDefault };
};

function setup() {
  const attach = vi.fn();
  const hook = renderHook(() => useFileDrop(attach));
  const on = () => hook.result.current.handlers;
  return { attach, hook, on, dragging: () => hook.result.current.dragging };
}

describe("useFileDrop", () => {
  it("stays dragging while the pointer crosses into a child and out again", () => {
    const { on, dragging } = setup();
    act(() => on().onDragEnter(drag(["Files"]).event));
    expect(dragging()).toBe(true);
    // Into a child: enter on the child first, then leave from the parent.
    act(() => on().onDragEnter(drag(["Files"]).event));
    act(() => on().onDragLeave());
    expect(dragging()).toBe(true);
    act(() => on().onDragLeave());
    expect(dragging()).toBe(false);
  });

  it("does not count below zero, so a stray leave cannot strand the next drag", () => {
    const { on, dragging } = setup();
    act(() => on().onDragLeave());
    act(() => on().onDragLeave());
    act(() => on().onDragEnter(drag(["Files"]).event));
    expect(dragging()).toBe(true);
    act(() => on().onDragLeave());
    expect(dragging()).toBe(false);
  });

  it("takes the drop, attaches its files, and starts the next drag from zero", () => {
    const { attach, on, dragging } = setup();
    act(() => on().onDragEnter(drag(["Files"]).event));
    act(() => on().onDragEnter(drag(["Files"]).event));
    const dropped = drag(["Files"], [file]);
    act(() => on().onDrop(dropped.event));
    expect(dropped.preventDefault).toHaveBeenCalled();
    expect(dragging()).toBe(false);
    expect(attach).toHaveBeenCalledWith([file]);

    act(() => on().onDragEnter(drag(["Files"]).event));
    act(() => on().onDragLeave());
    expect(dragging()).toBe(false);
  });

  it("ignores a drag that carries no files", () => {
    const { attach, on, dragging } = setup();
    const enter = drag(["text/plain"]);
    const over = drag(["text/plain"]);
    const dropped = drag(["text/plain"], [file]);
    act(() => {
      on().onDragEnter(enter.event);
      on().onDragOver(over.event);
      on().onDrop(dropped.event);
    });
    expect(dragging()).toBe(false);
    expect(attach).not.toHaveBeenCalled();
    expect(enter.preventDefault).not.toHaveBeenCalled();
    expect(over.preventDefault).not.toHaveBeenCalled();
    expect(dropped.preventDefault).not.toHaveBeenCalled();
  });

  it("claims a file drag passing over, so the drop lands here", () => {
    const { on } = setup();
    const over = drag(["Files"]);
    on().onDragOver(over.event);
    expect(over.preventDefault).toHaveBeenCalled();
  });
});
