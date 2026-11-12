import { useEffect, useLayoutEffect, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { parseAttachedFiles, type Artefact, type AttachedFile } from "~/lib/artefacts";
import { cn } from "~/lib/utils";
import type { PromptImage, QueuedPrompt } from "~/protocol";
import { useIsCoarsePointer } from "~/useMediaQuery";

import { PromptFiles, PromptImages } from "./Attachments";

// The editor grows with what is typed up to this, then scrolls.
const EDITOR_MAX_PX = 240;

// Text-sized on a mouse; a finger gets the full 44px of height.
const action =
  "hover:text-foreground focus-visible:ring-ring inline-flex items-center rounded-sm px-1 py-0.5 transition-colors outline-none focus-visible:ring-2 pointer-coarse:min-h-11";

// QueuedCard is a prompt the running turn has not read yet. It sits where it
// will land and looks like the user bubble it is about to become, dimmed, so
// the reader can see what is coming. One the harness already holds is read at
// its next step and cannot be taken back; one still waiting on the server can
// be reworded in place, sent ahead of its turn, or removed. The server holds
// it after a restart until the person resumes the queue, and after a usage
// limit until a new turn starts; the card says which, and for a limit, what it
// is waiting on: the continuation armed for the reset, or, with none armed,
// the reader sending something.
export function QueuedCard({
  queued,
  threadId,
  artefacts,
  limitHeld,
  busy,
  steers,
  onDequeue,
  onEdit,
  onSend,
  onUnsaved,
}: {
  queued: QueuedPrompt;
  threadId: string;
  artefacts: Artefact[];
  /** The latest turn stopped at a usage limit: nothing runs until the armed
      continuation goes out at the reset, or until the reader sends. */
  limitHeld?: "reset" | "send";
  /** A turn is running. */
  busy: boolean;
  /** The harness can take a message into the running turn. */
  steers: boolean;
  onDequeue: (queueId: string) => void;
  /** Resolves false when the server refused the edit, which keeps the editor
      open with what was typed. */
  onEdit: (queueId: string, prompt: string) => Promise<boolean> | boolean;
  /** Steers it into the running turn, or starts a turn with it when idle. */
  onSend: (queueId: string) => void;
  /** The editor's words while they differ from the prompt's, and undefined
      once they are saved, cancelled or put back. Not called when the card
      unmounts, so whoever listens still has them if the prompt leaves the
      queue mid-edit. */
  onUnsaved?: (text: string | undefined) => void;
}) {
  const [editing, setEditing] = useState(false);
  const prompt = queued.prompt ?? "";
  const { text, files } = parseAttachedFiles(prompt);
  // Everything after the words is the server's attached-files block, carried
  // back verbatim on a save so the files stay attached.
  const trailer = prompt.slice(text.length);
  const editable = !queued.sent;
  // Mid-turn only a harness that steers can take it now; idle, sending it
  // just starts the turn it was waiting for.
  const sendLabel = busy ? (steers ? "Steer now" : null) : "Send now";
  const images = queued.images ?? [];
  // A picture or a file can stand in for the words.
  const attached = images.length > 0 || files.length > 0;
  const open = editing && editable;

  return (
    <div data-queue-id={queued.queueId} className="fade-in flex flex-col items-end">
      <QueuedAttachments threadId={threadId} images={images} files={files} artefacts={artefacts} />
      {open ? (
        <QueuedEditor
          initial={text}
          // The words can go only if a picture or a file would still be sent.
          allowEmpty={attached}
          onSave={(next) => onEdit(queued.queueId, next + trailer)}
          onUnsaved={onUnsaved}
          onDone={() => setEditing(false)}
        />
      ) : (
        (text || !attached) && (
          <QueuedBubble text={text} onEdit={editable ? () => setEditing(true) : undefined} />
        )
      )}
      {!open && (
        <QueuedFooter
          queued={queued}
          limitHeld={limitHeld}
          sendLabel={sendLabel}
          onEdit={() => setEditing(true)}
          onSend={() => onSend(queued.queueId)}
          onRemove={() => onDequeue(queued.queueId)}
        />
      )}
    </div>
  );
}

// What rides with the prompt, dimmed like its words.
function QueuedAttachments({
  threadId,
  images,
  files,
  artefacts,
}: {
  threadId: string;
  images: PromptImage[];
  files: AttachedFile[];
  artefacts: Artefact[];
}) {
  return (
    <>
      {images.length > 0 && (
        <div className="opacity-60">
          <PromptImages threadId={threadId} images={images} />
        </div>
      )}
      {files.length > 0 && (
        <div className="flex w-full justify-end opacity-60">
          <PromptFiles files={files} artefacts={artefacts} />
        </div>
      )}
    </>
  );
}

const bubble =
  "bg-user-bubble text-user-bubble-foreground max-w-[85%] rounded-2xl rounded-br-md border border-dashed border-current/30 px-3.5 py-2 text-[14px] leading-relaxed break-words whitespace-pre-wrap";

// The words, dimmed, as the user bubble they are about to become. A tap opens
// the editor on one that can still be changed.
function QueuedBubble({ text, onEdit }: { text: string; onEdit?: () => void }) {
  if (!onEdit) return <div className={cn(bubble, "opacity-60")}>{text}</div>;
  return (
    <button
      type="button"
      onClick={onEdit}
      className={cn(bubble, "cursor-text text-left opacity-60 outline-none focus-visible:opacity-100")}
    >
      {text}
    </button>
  );
}

// Under the bubble: where the prompt stands, and what can still be done with
// it. Every part is spaced alike, the status included, so the dots between
// them sit evenly.
function QueuedFooter({
  queued,
  limitHeld,
  sendLabel,
  onEdit,
  onSend,
  onRemove,
}: {
  queued: QueuedPrompt;
  limitHeld?: "reset" | "send";
  sendLabel: string | null;
  onEdit: () => void;
  onSend: () => void;
  onRemove: () => void;
}) {
  return (
    <div className="text-muted-foreground mt-1 flex flex-wrap items-center justify-end gap-x-1 text-[12px]">
      {queued.sent ? (
        <span className="px-1">Sent · read after the current step</span>
      ) : (
        <>
          <span className="px-1">{queued.held ? "Held" : "Queued"}</span>
          <span aria-hidden>·</span>
          {limitHeld && (
            <>
              {/* Already "Held" when the queue is paused too; the limit
                  still holds it after a resume, so it says until when. */}
              <span className="px-1">
                {`${queued.held ? "" : "held "}${limitHeld === "reset" ? "until the limit resets" : "until you send"}`}
              </span>
              <span aria-hidden>·</span>
            </>
          )}
          <button type="button" onClick={onEdit} className={action}>
            Edit
          </button>
          {sendLabel && (
            <>
              <span aria-hidden>·</span>
              <button type="button" onClick={onSend} className={action}>
                {sendLabel}
              </button>
            </>
          )}
          <span aria-hidden>·</span>
          <button type="button" onClick={onRemove} className={action}>
            Remove
          </button>
        </>
      )}
    </div>
  );
}

// The bubble's words, edited where they sit. Save or Enter keeps them; Cancel
// or Escape puts the old ones back. A touch keyboard's Enter is a newline,
// since there is no Shift to reach for. Clicking away does not save: on a
// phone that is how the keyboard gets dismissed.
function QueuedEditor({
  initial,
  allowEmpty,
  onSave,
  onUnsaved,
  onDone,
}: {
  initial: string;
  allowEmpty: boolean;
  /** Resolves true when the edit landed. Not called when nothing changed. */
  onSave: (text: string) => Promise<boolean> | boolean;
  onUnsaved?: (text: string | undefined) => void;
  onDone: () => void;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [value, setValue] = useState(initial);
  const [saving, setSaving] = useState(false);
  const coarse = useIsCoarsePointer();
  const empty = !value.trim();
  const canSave = !saving && (allowEmpty || !empty);
  // The newline a touch keyboard's Enter left at the end is not part of it.
  const edited = (raw: string) => (raw.trim() ? raw.trimEnd() : "");

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus({ preventScroll: true });
    el.setSelectionRange(el.value.length, el.value.length);
    // The caret is at the end, so show the end of a prompt taller than the field.
    el.scrollTop = el.scrollHeight;
    // A focus scroll ignores the transcript's scroll padding and leaves Save
    // behind the floating composer; scrollIntoView honours it. A frame later,
    // so the field has been sized.
    const frame = requestAnimationFrame(() => wrapRef.current?.scrollIntoView({ block: "nearest" }));
    return () => cancelAnimationFrame(frame);
  }, []);

  // Sized by hand, as the composer is: Safari has no field-sizing. Collapsing
  // the field to measure it shortens the transcript for a moment, and both it
  // and the field are scrolled back up by the difference; their positions are
  // put back, or each keystroke drops the editor behind the composer.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const scroller = el.closest<HTMLElement>("[data-transcript-scroller]");
    const outer = scroller?.scrollTop ?? 0;
    const inner = el.scrollTop;
    el.style.height = "0px";
    const full = el.scrollHeight;
    el.style.height = `${Math.min(full, EDITOR_MAX_PX)}px`;
    el.style.overflowY = full > EDITOR_MAX_PX ? "auto" : "hidden";
    el.scrollTop = inner;
    if (scroller) scroller.scrollTop = outer;
  }, [value]);

  const finish = () => {
    onUnsaved?.(undefined);
    onDone();
  };

  const save = async () => {
    if (!canSave) return;
    const next = edited(value);
    if (next === initial) return finish();
    setSaving(true);
    // The server's reply is seconds away on a slow link; the editor holds
    // what was typed until it says yes, and stays up if it says no.
    let ok = false;
    try {
      ok = await onSave(next);
    } finally {
      setSaving(false);
    }
    if (ok) finish();
  };

  return (
    <div ref={wrapRef} className="flex w-full max-w-[85%] flex-col items-end gap-1.5">
      <textarea
        ref={ref}
        aria-label="Edit queued prompt"
        value={value}
        disabled={saving}
        onChange={(e) => {
          setValue(e.target.value);
          const next = edited(e.target.value);
          onUnsaved?.(next === initial ? undefined : next);
        }}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing || e.keyCode === 229) return;
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            if (!saving) finish();
          } else if (e.key === "Enter" && !e.shiftKey && !coarse) {
            e.preventDefault();
            void save();
          }
        }}
        // 16px on a phone: iOS zooms the page into any smaller field it focuses.
        className="bg-user-bubble text-user-bubble-foreground focus-visible:ring-ring/50 min-h-11 w-full resize-none rounded-2xl rounded-br-md border px-3.5 py-2 text-[16px] leading-relaxed outline-none focus-visible:ring-[3px] disabled:opacity-60 md:text-[14px]"
      />
      <div className="flex gap-1.5">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-9 pointer-coarse:min-h-11 md:h-7"
          disabled={saving}
          onClick={finish}
        >
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          className="h-9 pointer-coarse:min-h-11 md:h-7"
          disabled={!canSave}
          onClick={() => void save()}
        >
          Save
        </Button>
      </div>
    </div>
  );
}
