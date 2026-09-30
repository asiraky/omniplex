import type { RefObject } from "react";

import { overPromptLimit, sendPayload, type Attachment } from "~/lib/attachments";
import { submittedComposerAction } from "~/lib/composerItems";
import type { ComposerItem } from "~/protocol";

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
  catalogueReady: boolean;
  items: ComposerItem[];
  onSend: (text: string, attached: ComposerAttachments) => void;
  onCommandUsed?: (insertText: string) => void;
  runClientAction: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
}) {
  const uploading = attachments.some((a) => a.status === "uploading");
  const sendableAttachments = attachments.filter(
    (a) => a.status === "ready" || a.status === "staged",
  ).length;
  const carriesFiles = attachments.some((a) => a.kind === "file" && a.status !== "error");
  // The server would refuse it, and a refused send has already cleared the
  // composer by the time it says so.
  const tooMuch = overPromptLimit(attachments);
  const cannotSend =
    disabled ||
    sendDisabled ||
    uploading ||
    tooMuch ||
    (!draft.trim() && sendableAttachments === 0);

  const send = async () => {
    const t = draft.trim();
    // A message may be nothing but attachments: "what is this?" is often the
    // whole question, and the picture or the file is the rest of it.
    if ((!t && sendableAttachments === 0) || disabled || sendDisabled) return;
    // Sending now would send the message without the file still on its way up,
    // which is not what attaching it meant.
    if (uploading || tooMuch) return;
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
    onSend(t, sendPayload(attachments));
    changeDraft("");
  };

  return { send, cannotSend, sendableAttachments, carriesFiles, tooMuch };
}
