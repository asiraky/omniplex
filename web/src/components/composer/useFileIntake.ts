import { useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent } from "react";

import { dragHasFiles, filesFrom } from "~/lib/attachments";
import { pastedFile } from "~/lib/paste";

/** Files dragged onto the composer's card. */
export function useFileDrop(attach: (files: File[]) => void) {
  // Dragging over a child fires dragleave on the parent, so a boolean set from
  // those two events flickers as the pointer crosses the textarea. Depth counts
  // enters against leaves instead, and only zero means the drag has gone.
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  const handlers = {
    onDragEnter: (e: DragEvent<HTMLDivElement>) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      e.preventDefault();
      dragDepth.current++;
      setDragging(true);
    },
    onDragOver: (e: DragEvent<HTMLDivElement>) => {
      if (dragHasFiles(e.dataTransfer)) e.preventDefault();
    },
    onDragLeave: () => {
      if (dragDepth.current > 0) dragDepth.current--;
      if (dragDepth.current === 0) setDragging(false);
    },
    onDrop: (e: DragEvent<HTMLDivElement>) => {
      if (!dragHasFiles(e.dataTransfer)) return;
      e.preventDefault();
      dragDepth.current = 0;
      setDragging(false);
      attach(filesFrom(e.dataTransfer));
    },
  };

  return { dragging, handlers };
}

/**
 * Files pasted into the textarea, and long text pasted as a file. `noteKey`
 * and `clear` go on the textarea's keydown and keyup, to catch ⇧ on the
 * paste shortcut.
 */
export function usePasteIntake({
  attach,
  disabled,
  textAsFile,
}: {
  attach: (files: File[]) => void;
  disabled: boolean;
  /** Whether a long text paste may become a file at all. */
  textAsFile: boolean;
}) {
  // Set by ⌘⇧V / Ctrl⇧V between its keydown and the paste it causes.
  const plainPaste = useRef(false);

  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    // A screenshot on the clipboard is the fastest way to attach one, and
    // the reason the terminal habit transfers; a copied file comes the same
    // way.
    const files = filesFrom(e.clipboardData);
    if (files.length > 0) {
      e.preventDefault();
      attach(files);
      return;
    }
    // A long paste goes in as a file rather than a wall of text, unless it
    // came with ⇧, the usual "paste it as it is".
    const plain = plainPaste.current;
    plainPaste.current = false;
    if (plain || disabled || !textAsFile) return;
    const file = pastedFile(e.clipboardData.getData("text/plain"));
    if (!file) return;
    e.preventDefault();
    attach([file]);
  };

  const noteKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === "v")
      plainPaste.current = true;
  };

  const clear = () => {
    plainPaste.current = false;
  };

  return { onPaste, noteKey, clear };
}
