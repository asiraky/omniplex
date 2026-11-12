import type { RefObject } from "react";

import { overPromptLimit, sendPayload, type Attachment } from "~/lib/attachments";
import { submittedComposerAction } from "~/lib/composerItems";
import type { ComposerItem, Delivery } from "~/protocol";

import type { ComposerAttachments } from "../Composer";

/**
 * Whether the message can go, and sending it: as a prompt, or as the client or
 * adapter action its leading command names.
 */
export function useComposerSend({
  draft,
  draftRef,
  changeDraft,
  attachments,
  disabled,
  sendDisabled,
  waiting,
  catalogueReady,
  items,
  onSend,
  onCommandUsed,
  runClientAction,
  onRunComposerAction,
}: {
  draft: string;
  draftRef: RefObject<string>;
  changeDraft: (next: string) => void;
  attachments: Attachment[];
  disabled: boolean;
  sendDisabled: boolean;
  /** A message from this thread is held for its uploads. Another would
      overtake it, so nothing goes until it has. */
  waiting: boolean;
  catalogueReady: boolean;
  items: ComposerItem[];
  onSend: (text: string, attached: ComposerAttachments, delivery?: Delivery) => void;
  onCommandUsed?: (insertText: string) => void;
  runClientAction: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
}) {
  // A file still on its way up counts: the message is held until it lands,
  // so on a slow connection hitting send does not wait on the upload too.
  const sendableAttachments = attachments.filter((a) => a.status !== "error").length;
  const carriesFiles = attachments.some((a) => a.kind === "file" && a.status !== "error");
  // The server would refuse it, and a refused send has already cleared the
  // composer by the time it says so.
  const tooMuch = overPromptLimit(attachments);
  const cannotSend =
    disabled ||
    sendDisabled ||
    waiting ||
    tooMuch ||
    (!draft.trim() && sendableAttachments === 0);

  // `delivery` reaches only a prompt: a command runs now or not at all.
  const send = async (delivery?: Delivery) => {
    const t = draft.trim();
    // A message may be nothing but attachments: "what is this?" is often the
    // whole question, and the picture or the file is the rest of it.
    if ((!t && sendableAttachments === 0) || disabled || sendDisabled || waiting) return;
    if (tooMuch) return;
    if (t.startsWith("/") && !catalogueReady) return;
    // Recorded on submit rather than on completion: choosing from the menu is
    // browsing, sending is the use. The token is reported whatever the message
    // turns out to do — a prompt, a client action, an adapter action — because
    // all three are things the user reached for. Matched against the catalogue
    // rather than assumed to start with a slash: Codex's own skills trigger on
    // `$`, and a message beginning with a word that is not a command at all is
    // not a use of anything.
    const leading = t.split(/\s/, 1)[0] ?? "";
    const used = items.find((item) => item.insertText === leading);
    if (used) onCommandUsed?.(used.insertText);
    const intercepted = submittedComposerAction(t, items);
    if (intercepted?.item.behavior === "client-action") {
      changeDraft("");
      if (intercepted.item.action) runClientAction(intercepted.item.action);
      return;
    }
    if (intercepted?.item.behavior === "adapter-action" && intercepted.item.action) {
      try {
        const submittedDraft = draftRef.current;
        await onRunComposerAction?.(intercepted.item.action, intercepted.args, t);
        if (draftRef.current === submittedDraft) changeDraft("");
      } catch {
        // App reports the provider error. Retain the command so it can be
        // retried or edited, without leaving a rejected promise behind.
      }
      return;
    }
    onSend(t, sendPayload(attachments), delivery);
    changeDraft("");
  };

  return { send, cannotSend, sendableAttachments, carriesFiles, tooMuch };
}
