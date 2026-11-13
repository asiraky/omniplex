// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import type { DragEvent } from "react";
import { describe, expect, it, vi } from "vitest";

import { guardFileDrops, useFileDrop } from "./useFileDrop";

const file = new File(["x"], "x.png", { type: "image/png" });

/** A drag event as the handlers read it: what it carries, and whether it was taken. */
const drag = (types: string[], files: File[] = []) => {
  const preventDefault = vi.fn();
  const event = { dataTransfer: { types, files }, preventDefault } as unknown as DragEvent;
  return { event, preventDefault };
};

function setup(accepts = true) {
  const attach = vi.fn();
  const hook = renderHook(({ accepts }) => useFileDrop(attach, accepts), {
    initialProps: { accepts },
  });
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

  it("hands on nothing when a file drag drops no files after all", () => {
    const { attach, on } = setup();
    act(() => on().onDrop(drag(["Files"], []).event));
    expect(attach).not.toHaveBeenCalled();
  });

  it("neither lights up nor takes a drop while it does not accept", () => {
    const { attach, on, dragging } = setup(false);
    const enter = drag(["Files"]);
    const over = drag(["Files"]);
    act(() => {
      on().onDragEnter(enter.event);
      on().onDragOver(over.event);
    });
    expect(dragging()).toBe(false);
    expect(enter.preventDefault).not.toHaveBeenCalled();
    expect(over.preventDefault).not.toHaveBeenCalled();

    const dropped = drag(["Files"], [file]);
    act(() => on().onDrop(dropped.event));
    expect(dropped.preventDefault).not.toHaveBeenCalled();
    expect(attach).not.toHaveBeenCalled();
  });

  it("keeps counting while refusing, so it lights up right if it starts accepting mid-drag", () => {
    const { hook, on, dragging } = setup(false);
    act(() => on().onDragEnter(drag(["Files"]).event));
    hook.rerender({ accepts: true });
    expect(dragging()).toBe(true);
    act(() => on().onDragLeave());
    expect(dragging()).toBe(false);
  });
});

describe("guardFileDrops", () => {
  /** A real event on the window, carrying what a drag from the OS would. */
  const fire = (target: EventTarget, type: string, types: string[]) => {
    const event = new Event(type, { bubbles: true, cancelable: true });
    const dataTransfer = { types, dropEffect: "copy" };
    Object.defineProperty(event, "dataTransfer", { value: dataTransfer });
    target.dispatchEvent(event);
    return { prevented: event.defaultPrevented, dropEffect: dataTransfer.dropEffect };
  };

  it("refuses a file drag nothing took, and keeps its drop from opening the file", () => {
    const stop = guardFileDrops(window);
    try {
      expect(fire(document.body, "dragover", ["Files"])).toEqual({
        prevented: true,
        dropEffect: "none",
      });
      expect(fire(document.body, "drop", ["Files"]).prevented).toBe(true);
    } finally {
      stop();
    }
  });

  it("leaves a drag an element took alone, and anything that is not files", () => {
    const stop = guardFileDrops(window);
    const take = (e: Event) => e.preventDefault();
    document.body.addEventListener("dragover", take);
    try {
      expect(fire(document.body, "dragover", ["Files"]).dropEffect).toBe("copy");
      document.body.removeEventListener("dragover", take);
      expect(fire(document.body, "dragover", ["text/plain"])).toEqual({
        prevented: false,
        dropEffect: "copy",
      });
      expect(fire(document.body, "drop", ["text/plain"]).prevented).toBe(false);
    } finally {
      document.body.removeEventListener("dragover", take);
      stop();
    }
  });

  it("lets go of the window when stopped", () => {
    guardFileDrops(window)();
    expect(fire(document.body, "dragover", ["Files"]).prevented).toBe(false);
  });
});
