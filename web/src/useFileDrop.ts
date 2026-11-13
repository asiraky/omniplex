import { useRef, useState, type DragEvent } from "react";

import { dragHasFiles, filesFrom } from "~/lib/attachments";

/**
 * Files dragged from the OS onto an element: the composer's card, or a
 * thread's row in the sidebar. Only a drag that carries files counts, so a
 * row's pointer-driven reorder never reaches these handlers.
 */
export function useFileDrop(attach: (files: File[]) => void) {
  // Dragging over a child fires dragleave on the parent, so a boolean set from
  // those two events flickers as the pointer crosses the textarea. Depth counts
  // enters against leaves instead, and only zero means the drag has gone.
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  const handlers = {
    onDragEnter: (e: DragEvent) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      e.preventDefault();
      dragDepth.current++;
      setDragging(true);
    },
    onDragOver: (e: DragEvent) => {
      if (dragHasFiles(e.dataTransfer)) e.preventDefault();
    },
    onDragLeave: () => {
      if (dragDepth.current > 0) dragDepth.current--;
      if (dragDepth.current === 0) setDragging(false);
    },
    onDrop: (e: DragEvent) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      e.preventDefault();
      dragDepth.current = 0;
      setDragging(false);
      attach(filesFrom(e.dataTransfer));
    },
  };

  return { dragging, handlers };
}
