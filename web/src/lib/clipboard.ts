import { useCallback, useEffect, useRef, useState } from "react";

import { toast } from "./toast";

/**
 * Copies through the async Clipboard API, falling back to the legacy
 * `execCommand` path.
 *
 * `navigator.clipboard` exists only in a secure context, and omniplex is routinely
 * reached over plain http on a LAN address — which is exactly where the phone
 * is. There the whole `clipboard` object is undefined, so the obvious
 * `navigator.clipboard?.writeText(...)` reads as "no clipboard, do nothing"
 * and the button dies silently under a thumb. The old selection-based copy
 * still works in that context, so we use it rather than telling the reader
 * their browser cannot copy.
 */
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // A permissions policy, a non-user gesture, or Safari losing the user
      // activation across an await — all recoverable by the legacy path.
    }
  }
  if (!legacyCopy(text)) throw new Error("This browser blocked the copy");
}

/**
 * The pre-Clipboard-API copy: select text in an offscreen field, then ask the
 * document to copy the selection.
 *
 * The details are load-bearing. The field has to be focused before the
 * selection is made — Blink only caches `setSelectionRange` on an unfocused
 * text control and never touches the document selection, so an unfocused field
 * copies nothing. It has to stay `readonly` so focusing it cannot raise the
 * keyboard on a phone, and 16px so focusing it cannot zoom the page in Mobile
 * Safari. It is positioned offscreen rather than hidden because a field with
 * no layout cannot hold a selection at all.
 */
function legacyCopy(text: string): boolean {
  const selection = window.getSelection();
  // Whatever the reader had selected, and whatever had focus — usually the
  // composer — is theirs. Put both back rather than leaving our scratch field's
  // state behind.
  const previousRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null;
  const previouslyFocused = document.activeElement;

  const field = document.createElement("textarea");
  field.value = text;
  field.setAttribute("readonly", "");
  field.style.cssText =
    "position:fixed;top:0;left:-9999px;width:1px;height:1px;padding:0;border:0;font-size:16px;";
  document.body.appendChild(field);

  try {
    field.focus({ preventScroll: true });
    field.setSelectionRange(0, field.value.length);
    // `execCommand("copy")` reports success even when there was no selection to
    // copy, so its return value alone cannot be trusted: check that the field
    // really is selected first, or a silent failure becomes a lying "Copied".
    if (text.length > 0 && field.selectionEnd - field.selectionStart !== text.length) return false;
    return document.execCommand?.("copy") ?? false;
  } catch {
    return false;
  } finally {
    field.remove();
    selection?.removeAllRanges();
    if (previousRange) selection?.addRange(previousRange);
    if (previouslyFocused instanceof HTMLElement) previouslyFocused.focus({ preventScroll: true });
  }
}

// How long the button keeps saying "copied" before returning to its resting
// state. Long enough to be read, short enough not to linger over the content.
const COPIED_MS = 1500;

/**
 * Copy state for a button: the transient "copied" flag plus the copy itself,
 * with the feedback both ways. The check on the button is the quiet signal and
 * the toast is the loud one — on a phone the button is often under the thumb
 * that pressed it, so the check alone is a copy the reader cannot see.
 */
export function useCopy(): { copied: boolean; copy: (text: string) => Promise<void> } {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timer.current), []);

  const copy = useCallback(async (text: string) => {
    try {
      await copyText(text);
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), COPIED_MS);
      toast.success("Copied");
    } catch (e) {
      toast.error("Could not copy", { description: e instanceof Error ? e.message : String(e) });
    }
  }, []);

  return { copied, copy };
}
