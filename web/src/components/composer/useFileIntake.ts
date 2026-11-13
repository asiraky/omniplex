import { useRef, type ClipboardEvent, type KeyboardEvent } from "react";

import { filesFrom } from "~/lib/attachments";
import { pastedFile, pasteTakesText } from "~/lib/paste";

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
    // way. Files that come with real text, as cells from a spreadsheet do
    // with a picture of themselves, go in as the text.
    const files = filesFrom(e.clipboardData);
    if (files.length > 0 && !pasteTakesText(e.clipboardData)) {
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
