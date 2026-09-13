// Folding auth_event frames into what the sign-in dialog shows. Deliberately
// a pure reducer beside the components: a flow is ephemeral narration bound to
// one connection, so it never touches the session reducer or any persisted
// state — it lives in a dialog's useState and dies with it.

import type { AuthFlowEvent, AuthFlowNotice, AuthFlowPrompt } from "~/protocol";

export interface AuthFlowView {
  /** Everything narrated so far, oldest first. */
  notices: AuthFlowNotice[];
  /** The question waiting for an answer, if any. */
  prompt: AuthFlowPrompt | null;
  done: boolean;
  error: string | null;
}

export function emptyAuthFlowView(): AuthFlowView {
  return { notices: [], prompt: null, done: false, error: null };
}

export function applyAuthFlowEvent(view: AuthFlowView, ev: AuthFlowEvent): AuthFlowView {
  let next = view;
  if (ev.event) {
    const notices = [...next.notices];
    // Progress lines update in place rather than stacking: "Waiting…" ten
    // times is one fact, not ten. Anything else is worth keeping on screen.
    if (ev.event.type === "progress" && notices[notices.length - 1]?.type === "progress") {
      notices[notices.length - 1] = ev.event;
    } else {
      notices.push(ev.event);
    }
    next = { ...next, notices };
  }
  if (ev.prompt) next = { ...next, prompt: ev.prompt };
  if (ev.error) {
    // Terminal either way; a dangling prompt under an error would invite an
    // answer nobody is listening for.
    next = { ...next, error: ev.error, done: true, prompt: null };
  } else if (ev.done) {
    next = { ...next, done: true, prompt: null };
  }
  return next;
}

/** The prompt was answered: take it off screen while the flow digests it. */
export function answeredPrompt(view: AuthFlowView): AuthFlowView {
  return view.prompt ? { ...view, prompt: null } : view;
}
