import { useEffect, useRef, useState, type DragEvent } from "react";

import { dragHasFiles, filesFrom } from "~/lib/attachments";

/**
 * Files dragged from the OS onto an element: the composer's card, or a
 * thread's row in the sidebar. Only a drag that carries files counts, so a
 * row's pointer-driven reorder never reaches these handlers, and `attach` is
 * never called with none.
 *
 * While `accepts` is false the element neither lights up nor takes the drop,
 * and the window's guard (`guardFileDrops`) refuses it. Its handlers stay on
 * all the same, so the drag is still counted and the element lights up
 * correctly if it starts accepting mid-drag.
 */
export function useFileDrop(attach: (files: File[]) => void, accepts = true) {
  // Dragging over a child fires dragleave on the parent, so a boolean set from
  // those two events flickers as the pointer crosses the element's children.
  // Depth counts enters against leaves instead, and only zero means the drag
  // has gone.
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  const handlers = {
    onDragEnter: (e: DragEvent) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      if (accepts) e.preventDefault();
      dragDepth.current++;
      setDragging(true);
    },
    onDragOver: (e: DragEvent) => {
      if (accepts && dragHasFiles(e.dataTransfer)) e.preventDefault();
    },
    onDragLeave: () => {
      if (dragDepth.current > 0) dragDepth.current--;
      if (dragDepth.current === 0) setDragging(false);
    },
    onDrop: (e: DragEvent) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      dragDepth.current = 0;
      setDragging(false);
      if (!accepts) return;
      e.preventDefault();
      const files = filesFrom(e.dataTransfer);
      if (files.length > 0) attach(files);
    },
  };

  return { dragging: dragging && accepts, handlers };
}

/**
 * Refuses a file drag that nothing on the page took. Left alone, a file let go
 * anywhere else (between two rows, on a header, on a thread that cannot take
 * it) is the browser's to open, and it opens it in place of the app. The
 * cursor says no instead. Elements that take files cancel the drag
 * themselves before it bubbles up here. Returns the cleanup.
 */
export function guardFileDrops(target: Window) {
  const refuseOver = (e: globalThis.DragEvent) => {
    if (e.defaultPrevented || !dragHasFiles(e.dataTransfer)) return;
    e.preventDefault();
    e.dataTransfer!.dropEffect = "none";
  };
  const refuseDrop = (e: globalThis.DragEvent) => {
    if (dragHasFiles(e.dataTransfer)) e.preventDefault();
  };
  target.addEventListener("dragover", refuseOver);
  target.addEventListener("drop", refuseDrop);
  return () => {
    target.removeEventListener("dragover", refuseOver);
    target.removeEventListener("drop", refuseDrop);
  };
}

/** `guardFileDrops` on the window for as long as the app is up. */
export function useGuardFileDrops() {
  useEffect(() => guardFileDrops(window), []);
}
