import { PaperclipIcon } from "lucide-react";
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from "react";

import type { StashEntry } from "~/app/stashStore";
import { ContextMeter } from "~/components/ContextMeter";
import type { Attachment } from "~/lib/attachments";
import type { ArtefactRef } from "~/lib/artefacts";
import type { PickerFocus } from "~/components/ModelPicker";
import { isComposing } from "~/lib/ime";
import { recentPrompts, stepHistory, type HistoryPosition } from "~/lib/promptHistory";
import { typeInto } from "~/lib/typeInto";
import { currentPlatform, formatShortcut } from "~/lib/keybindings";
import type { PickerInstance } from "~/lib/models";
import { cn } from "~/lib/utils";
import type { Delivery, HarnessMeta, Usage } from "~/protocol";
import { useLatest } from "~/useLatest";
import { useIsDesktop } from "~/useMediaQuery";

import { AttachButton } from "./composer/AttachButton";
import { AttachmentStrip } from "./composer/AttachmentStrip";
import { CommandMenu } from "./composer/CommandMenu";
import { caretOnEdge } from "./composer/caretEdge";
import { ComposerHistoryMenu, MENU_RECENT } from "./composer/ComposerHistoryMenu";
import { ComposerModelPicker } from "./composer/ComposerModelPicker";
import { ComposerNotes } from "./composer/ComposerNotes";
import { DELIVERY_LABEL, deliveryFor, offersChoice } from "./composer/delivery";
import { SendButtons } from "./composer/SendButtons";
import { NO_CATALOGUE, type ComposerCatalogue } from "./composer/useComposerItems";
import { useComposerMenu } from "./composer/useComposerMenu";
import { useComposerFit } from "./composer/useComposerFit";
import { useComposerSend } from "./composer/useComposerSend";
import { useDraftRef } from "./composer/useDraftRef";
import { useFileDrop, usePasteIntake } from "./composer/useFileIntake";

/** Where the composer put its extra tools: its toolbar, or the drawer under it. */
export type ToolPlace = "row" | "drawer";

/** What a message carries besides its text: the ready images by id, and the
    ready files by the artefact each became. */
export interface ComposerAttachments {
  imageIds: string[];
  files: ArtefactRef[];
}

/** What the transcript's recent-skills list and the app's shortcuts need from the composer. */
export interface ComposerHandle {
  /** Focuses the input and parks the cursor at `cursor`, or at the end. */
  focusEnd: (cursor?: number) => void;
  /** Focuses the input and types `text` at the end of the draft, where undo
      can take it back out. False when the browser would not take it; the
      caller then writes the draft itself. */
  typeAtEnd: (text: string) => boolean;
  /** Opens the model picker; "effort" puts focus on the effort control. Already open: moves focus there. */
  openModelPicker: (focus?: PickerFocus) => void;
  /** Sends the default way, then opens a new thread's draft, as ⇧⌘↵ does.
      Does nothing where the composer has no `onSendAndNewThread`. */
  sendAndNewThread: () => void;
  /** Sends `text` as though it had been typed and submitted: a leading
      command runs as one. */
  send: (text: string) => void;
}

/** A thread's parked drafts, and what the composer's menu can do with them. */
export interface ComposerStash {
  /** Newest first. */
  entries: readonly StashEntry[];
  /** Parks the draft's text and ready attachments, emptying the composer;
      with `textOnly`, the attachments stay. False when there was nothing to
      park. */
  save: (textOnly?: boolean) => boolean;
  /** Puts a parked draft back, parking whatever is in the composer first. */
  take: (id: string) => StashEntry | undefined;
  drop: (id: string) => void;
}

const NO_PROMPTS: readonly string[] = [];

interface ComposerProps {
  ref?: Ref<ComposerHandle>;
  /**
   * The in-progress message. Owned by the parent and keyed per thread there,
   * so it survives this component being unmounted and remounted across a
   * thread switch — the draft is not this component's to lose.
   */
  draft: string;
  onDraftChange: (text: string) => void;
  disabled: boolean;
  /** Blocks sending without locking the input. The workspace being prepared is
      not a reason to stop someone writing the first message — only a reason to
      hold it back until there is something to send it to. */
  sendDisabled?: boolean;
  busy: boolean;
  /** The thread's harness can take a message mid-turn. While a turn runs a
      send steers it by default, and ⌘↵ or holding Send queues instead. */
  steers?: boolean;
  /** `delivery` is set only for a send the other way from the default. */
  onSend: (text: string, attached: ComposerAttachments, delivery?: Delivery) => void;
  /** Opens a new thread's draft once a send from here has gone. Present only
      on an existing thread's composer, where it puts "send and start a new
      thread" under Send: held on a phone, ⇧⌘↵ on a keyboard. Never called for
      a send that was held back. */
  onSendAndNewThread?: () => void;
  onSchedule?: () => void;
  onCancel: () => void;
  /** Images and files staged for the next message. Owned by the parent for
      the same reason the draft is: a thread switch unmounts this component. */
  attachments?: Attachment[];
  /** Hands picked, dropped, or pasted files of any type to the parent, which
      uploads them — images on the image path, everything else as artefacts.
      Anything that is not a file is left to the textarea. */
  onAttachFiles?: (files: File[]) => void;
  onRemoveAttachment?: (key: string) => void;
  /** Puts a failed upload up again. Omitted, a failed chip offers only removal. */
  onRetryAttachment?: (key: string) => void;
  /** A message from this thread went with files still uploading and is held
      until they land. Send waits too, or the next message would overtake it;
      writing it does not. */
  waiting?: boolean;
  disabledPlaceholder?: string;
  /** Every harness the server reports; the picker reads this thread's out. */
  harnesses?: HarnessMeta[];
  /** The attached thread's harness, which it cannot change, and account,
      which it can — to another account of the same harness. */
  harness?: string;
  instance?: string;
  model?: string;
  effort?: string;
  /** The thread's service tier; "" is standard speed. */
  serviceTier?: string;
  /** Mid-thread these resolve to whether the server took the change. */
  onSwitchModel?: (id: string) => void | Promise<boolean>;
  onSwitchEffort?: (effort: string) => void | Promise<boolean>;
  onSwitchServiceTier?: (serviceTier: string) => void | Promise<boolean>;
  /** Moves the thread to another account of its harness, then runs `model`
      there. Omitted, the picker still offers other accounts but choosing one
      does nothing. */
  onSwitchAccount?: (instance: string, model: string) => void;
  /** A thread not yet started can pick any harness, not only its own. */
  anyHarness?: boolean;
  /** Choosing an account on the picker's rail, before any model under it. */
  onPickInstance?: (instance: PickerInstance) => void;
  /** The thread's token usage, source of the context meter. */
  usage?: Usage;
  /** The provider's command catalogue, from `useComposerItems` in the parent.
      Omitted, the menu offers only the composer's own commands. */
  catalogue?: ComposerCatalogue;
  onRunClientAction?: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
  /** Reports the leading `/token` of a submitted message, so the parent can
      remember which skills this user actually reaches for. */
  onCommandUsed?: (insertText: string) => void;
  /** The last message came back unsent: why, shown over the composer, how to
      send it again, and how to put the note away. No `onRetry` for one that
      cannot go again as it was, such as a message whose file failed to upload. */
  failure?: { reason: string; onRetry?: () => void; onDismiss: () => void };
  /**
   * Extra controls, right after the model picker in the toolbar while there
   * is room for them and the model's name both, and first in the drawer under
   * the box once there is not. Told which, so each can size itself to it.
   */
  tools?: (place: ToolPlace) => ReactNode;
  /** A drawer under the box, for context that is usually right already. */
  footer?: ReactNode;
  /** Present when the server can transcribe (an ElevenLabs key is stored), and
      names the thread a transcript is for. The new-thread screen has no thread
      yet, so it passes an empty object. */
  dictation?: { thread?: string };
  /** The prompts sent in this thread, oldest first. ArrowUp in an empty
      composer walks back through them; the menu lists the latest. */
  history?: readonly string[];
  /** The thread's parked drafts. Without it the composer has no menu, for
      parked drafts or for recent prompts. */
  stash?: ComposerStash;
}

const NO_HARNESSES: HarnessMeta[] = [];

const DictationControls = lazy(() => import("./composer/DictationControls"));

/** Whether this browser can record at all. An insecure origin hides
    `mediaDevices`, so a plain-http LAN address answers no here. */
function canDictate(): boolean {
  return (
    typeof MediaRecorder !== "undefined" &&
    typeof navigator !== "undefined" &&
    typeof navigator.mediaDevices?.getUserMedia === "function"
  );
}

const NO_ATTACHMENTS: Attachment[] = [];
// The tallest the composer grows before it scrolls. Matches max-h-[200px] on
// the textarea, which has to stay a literal for Tailwind to see it.
const COMPOSER_MAX_PX = 200;

function placeholderFor(
  blocked: boolean,
  disabledPlaceholder: string | undefined,
  isDesktop: boolean,
  choice: boolean,
) {
  if (blocked) return disabledPlaceholder ?? "Thread closed";
  // There is no ⇧↵ worth advertising on a phone, so keep the hint to desktop.
  if (!isDesktop) return "Ask anything…";
  if (!choice) return "Ask anything…  (↵ to send · ⇧↵ for newline)";
  const name = (d: Delivery) => DELIVERY_LABEL[d].name.toLowerCase();
  return `Ask anything…  (↵ to ${name("steer")} · ${formatShortcut({ key: "enter", mod: true }, currentPlatform())} to ${name("queue")} · ⇧↵ for newline)`;
}

export function Composer({
  ref,
  draft,
  onDraftChange,
  disabled,
  sendDisabled = false,
  busy,
  steers = false,
  onSend,
  onSendAndNewThread,
  onSchedule,
  onCancel,
  attachments = NO_ATTACHMENTS,
  onAttachFiles,
  onRemoveAttachment,
  onRetryAttachment,
  waiting = false,
  disabledPlaceholder,
  harnesses = NO_HARNESSES,
  harness = "",
  instance = "",
  model = "",
  effort = "",
  serviceTier = "",
  onSwitchModel,
  onSwitchEffort,
  onSwitchServiceTier,
  onSwitchAccount,
  anyHarness = false,
  onPickInstance,
  usage,
  catalogue = NO_CATALOGUE,
  onRunClientAction,
  onRunComposerAction,
  onCommandUsed,
  failure,
  tools,
  footer,
  dictation,
  history = NO_PROMPTS,
  stash,
}: ComposerProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const fit = useComposerFit(boxRef);
  const isDesktop = useIsDesktop();
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  // Where a shortcut asked focus to go. Cleared on close, so opening by a
  // plain click afterwards lands on the search as usual.
  const [pickerFocus, setPickerFocus] = useState<{ target: PickerFocus } | null>(null);
  const changeModelPickerOpen = useCallback((next: boolean) => {
    setModelPickerOpen(next);
    if (!next) setPickerFocus(null);
  }, []);
  const { draftRef, changeDraft } = useDraftRef(draft, onDraftChange);

  const attach = useCallback(
    (files: File[]) => {
      if (disabled || files.length === 0) return;
      onAttachFiles?.(files);
    },
    [disabled, onAttachFiles],
  );
  const drop = useFileDrop(attach);
  const paste = usePasteIntake({ attach, disabled, textAsFile: !!onAttachFiles });

  const runClientAction = useCallback(
    (action: string) => {
      if (action === "model") {
        // Let the command popover close before opening the picker. Focusing
        // the textarea here would immediately dismiss the newly opened picker.
        window.requestAnimationFrame(() => setModelPickerOpen(true));
      } else {
        onRunClientAction?.(action);
      }
    },
    [onRunClientAction],
  );

  // How far back ArrowUp has gone through the thread's prompts. It holds only
  // while the composer says exactly what was recalled: edit it, and it is a
  // draft like any other, which ArrowUp leaves alone.
  const [recall, setRecall] = useState<HistoryPosition | null>(null);
  // A recalled `/review` is not a command being typed: the menu would open on
  // it and take the next arrow key, stopping the walk through history there.
  const walking = recall !== null && recall.recalled === draft;

  const menu = useComposerMenu({
    draft,
    draftRef,
    changeDraft,
    catalogue,
    textareaRef,
    disabled: disabled || walking,
    sendDisabled,
    runClientAction,
    onRunComposerAction,
  });
  const { send, cannotSend, sendableAttachments, scheduleBlock, tooMuch } = useComposerSend({
    draft,
    draftRef,
    changeDraft,
    attachments,
    disabled,
    sendDisabled,
    waiting,
    catalogueReady: catalogue.ready,
    items: menu.items,
    onSend,
    onCommandUsed,
    runClientAction,
    onRunComposerAction,
  });
  const sendAndNew = onSendAndNewThread && (() => void send(undefined, onSendAndNewThread));
  // Through refs, so the handle is not rebuilt every render, as `send` is.
  const latestSendAndNew = useLatest(sendAndNew);
  const sendRef = useLatest(send);

  // Grow with the content, up to a cap. Runs on mount too, so a restored draft
  // opens at the right height instead of a single collapsed row.
  //
  // Scrolling is switched on only once the content passes the cap. Below it
  // the box already fits everything, but Safari still draws a scrollbar
  // across an empty composer, so overflow stays hidden until there is
  // something to scroll to.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0px";
    const full = el.scrollHeight;
    el.style.height = `${Math.min(full, COMPOSER_MAX_PX)}px`;
    el.style.overflowY = full > COMPOSER_MAX_PX ? "auto" : "hidden";
  }, [draft]);

  const { focusAt, setCursor } = menu;
  const hasPicker = harnesses.length > 0 && !disabled;

  // Replaces the whole draft. Typed in where it can be, so undo takes it back
  // out. Without focus it is written instead: focusing would raise a phone's
  // keyboard over the menu that was just tapped.
  const replaceDraft = useCallback(
    (text: string, focus: boolean) => {
      const el = textareaRef.current;
      if (focus && el && typeInto(el, 0, el.value.length, text)) return;
      changeDraft(text);
      if (focus) focusAt(text.length);
    },
    [changeDraft, focusAt],
  );
  // Only at the textarea's top or bottom line, wrapped lines included, so the
  // arrows still move the caret through a recalled prompt of several lines.
  const stepRecall = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    const back = e.key === "ArrowUp";
    if (!back && e.key !== "ArrowDown") return false;
    if (e.shiftKey || e.altKey || e.metaKey || e.ctrlKey || history.length === 0) return false;
    const el = e.currentTarget;
    const step = stepHistory({
      direction: back ? "back" : "forward",
      entries: history,
      position: recall,
      current: el.value,
    });
    if (!step || !caretOnEdge(el, back ? "top" : "bottom")) return false;
    e.preventDefault();
    setRecall(step.position);
    replaceDraft(step.text, true);
    return true;
  };
  const recent = useMemo(() => recentPrompts(history, MENU_RECENT), [history]);
  const hasReady = attachments.some((a) => a.status === "ready");
  const placeCaret = useCallback(
    (cursor: number, focus: boolean) => {
      // Without focus the caret is left to the browser: placing it would pull
      // the phone keyboard up over a transcript the person only wanted to read.
      if (focus) focusAt(cursor);
      else setCursor(cursor);
    },
    [focusAt, setCursor],
  );
  // Not just the key: a browser without MediaRecorder, or a page served over
  // plain http, which hides the microphone entirely, cannot record.
  const showMic = !!dictation && canDictate();
  const modelControls = (
    <>
      {harnesses.length > 0 && (
        <ComposerModelPicker
          harnesses={harnesses}
          harness={harness}
          instance={instance}
          model={model}
          effort={effort}
          serviceTier={serviceTier}
          contextWindow={usage?.contextWindow}
          anyHarness={anyHarness}
          disabled={disabled}
          open={modelPickerOpen}
          onOpenChange={changeModelPickerOpen}
          focusRequest={pickerFocus}
          onSwitchModel={onSwitchModel}
          onSwitchEffort={onSwitchEffort}
          onSwitchServiceTier={onSwitchServiceTier}
          onSwitchAccount={onSwitchAccount}
          onPickInstance={onPickInstance}
          compact={fit.compactTrigger}
          ownLine={fit.triggerOwnLine}
        />
      )}
      {fit.toolsInRow && tools?.("row")}
    </>
  );
  // Icons, mostly, so they go ahead of the drawer's long labels, where a
  // phone-width drawer would otherwise scroll them out of sight.
  const drawerTools = !fit.toolsInRow && tools?.("drawer");
  const meter = usage && (usage.contextUsed ?? 0) > 0 && <ContextMeter usage={usage} model={model} />;
  const noMic = (
    <>
      {modelControls}
      <span className="flex-1" />
      {meter}
    </>
  );

  useImperativeHandle(
    ref,
    () => ({
      focusEnd: (nextCursor?: number) => focusAt(nextCursor ?? draftRef.current.length),
      typeAtEnd: (text: string) => {
        const el = textareaRef.current;
        if (!el || el.value !== draftRef.current) return false;
        return typeInto(el, el.value.length, el.value.length, text);
      },
      openModelPicker: (focus: PickerFocus = "model") => {
        if (!hasPicker) return;
        // A fresh object each time, so asking again while open moves focus.
        setPickerFocus({ target: focus });
        setModelPickerOpen(true);
      },
      sendAndNewThread: () => latestSendAndNew.current?.(),
      send: (text: string) => void sendRef.current(undefined, undefined, text),
    }),
    [draftRef, focusAt, hasPicker, latestSendAndNew, sendRef],
  );

  const textarea = (
    <textarea
      ref={textareaRef}
      rows={1}
      value={draft}
      disabled={disabled}
      aria-label="Message"
      data-composer-input=""
      placeholder={placeholderFor(
        disabled || sendDisabled,
        disabledPlaceholder,
        isDesktop,
        offersChoice(busy, steers),
      )}
      onChange={(e) => {
        changeDraft(e.target.value);
        menu.setCursor(e.target.selectionStart);
      }}
      onPaste={paste.onPaste}
      onFocus={() => menu.setFocused(true)}
      onBlur={() => menu.setFocused(false)}
      onClick={(e) => menu.setCursor(e.currentTarget.selectionStart)}
      onKeyUp={(e) => {
        paste.clear();
        menu.setCursor(e.currentTarget.selectionStart);
      }}
      onKeyDown={(e) => {
        paste.noteKey(e);
        if (isComposing(e)) return;
        if (menu.onKey(e)) return;
        if (stepRecall(e)) return;
        if (e.key !== "Enter") return;
        // Shift+Enter is the newline; let the textarea handle it. ⇧⌘↵ in an
        // open thread never gets here: the app's shortcut takes it first.
        if (e.shiftKey) return;
        e.preventDefault();
        // ⌘↵ (Ctrl↵ off a Mac) sends the other way from the default.
        void send(deliveryFor({ steers, inverted: e.metaKey || e.ctrlKey }));
      }}
      // 16px on a phone: anything smaller makes iOS zoom the viewport on
      // focus, which breaks the layout the dvh handling just fixed.
      className="scroll-thin placeholder:text-muted-foreground max-h-[200px] min-h-16 w-full resize-none overflow-x-hidden bg-transparent px-4 pt-3.5 pb-1 text-[16px] leading-relaxed focus:outline-none disabled:opacity-60 md:text-[14px]"
    />
  );

  return (
    <div className="mx-auto max-w-3xl px-4 pb-[calc(0.875rem+env(safe-area-inset-bottom))] md:px-5">
      <div
        ref={boxRef}
        className={cn(
          "bg-card focus-within:border-ring focus-within:ring-ring/50 relative z-10 rounded-2xl border shadow-lg transition-[color,box-shadow] focus-within:ring-[3px]",
          drop.dragging && "border-primary ring-primary/50 ring-[3px]",
        )}
        {...drop.handlers}
      >
        {drop.dragging && <DropHint />}
        <ComposerNotes waiting={waiting} failure={failure} canSend={!disabled && !sendDisabled} />
        <AttachmentStrip
          attachments={attachments}
          tooMuch={tooMuch}
          onRemove={onRemoveAttachment}
          onRetry={onRetryAttachment}
        />
        <CommandMenu
          open={menu.open}
          anchor={textarea}
          anchorRef={textareaRef}
          onDismiss={menu.dismiss}
          matches={menu.matches}
          activeIndex={menu.activeIndex}
          loading={catalogue.loading}
          onHover={menu.setActiveIndex}
          onChoose={menu.choose}
        />

        <div
          className={cn(
            "flex items-center gap-1 px-2.5 pb-2",
            // Tighter, so attach, meter, mic, stop and send fit one line under it.
            fit.triggerOwnLine && "flex-wrap gap-x-0.5",
          )}
        >
          <AttachButton show={!!onAttachFiles} disabled={disabled} onFiles={attach} />
          {stash && (
            <ComposerHistoryMenu
              disabled={disabled}
              canStash={!!draft.trim() || hasReady}
              stashed={stash.entries}
              recent={recent}
              onStash={() => void stash.save()}
              onTake={(id) => {
                setRecall(null);
                stash.take(id);
              }}
              onDrop={stash.drop}
              onRecall={(text) => {
                // Words typed in the composer are parked rather than lost: a
                // phone has no undo to get them back. A prompt still listed
                // in this menu is not parked again, and the attachments stay
                // to go out with the recalled prompt.
                const current = draftRef.current.trim();
                if (current && !recent.includes(current)) stash.save(true);
                setRecall(null);
                replaceDraft(text, isDesktop);
              }}
              // On desktop the next keystroke belongs in the composer: what
              // to send instead of a stashed draft, or an edit to a recalled
              // one. A phone stays unfocused, keeping its keyboard down.
              onPicked={() => {
                if (isDesktop) focusAt(draftRef.current.length);
              }}
            />
          )}

          {showMic ? (
            // Until the recorder's code arrives the row is laid out as if
            // there were no mic.
            <Suspense fallback={noMic}>
              <DictationControls
                thread={dictation?.thread}
                textareaRef={textareaRef}
                draftRef={draftRef}
                changeDraft={changeDraft}
                placeCaret={placeCaret}
                disabled={disabled}
                controls={modelControls}
                meter={meter}
              />
            </Suspense>
          ) : (
            noMic
          )}

          <SendButtons
            busy={busy}
            steers={steers}
            hasContent={!!draft.trim() || sendableAttachments > 0}
            cannotSend={cannotSend}
            scheduleBlock={scheduleBlock}
            onSend={(delivery) => void send(delivery)}
            onSendAndNew={sendAndNew}
            onSchedule={onSchedule}
            onCancel={onCancel}
          />
        </div>
      </div>
      {(footer || drawerTools) && (
        // A drawer pulled out from under the box: narrower than it, tucked
        // behind its bottom edge, so it reads as part of the composer.
        <div className="bg-muted/50 mx-2.5 -mt-3 rounded-b-xl border border-t-0 pt-3">
          <div
            role="group"
            aria-label="Thread options"
            className="flex flex-wrap gap-0.5 py-0.5"
          >
            {drawerTools}
            {footer}
          </div>
        </div>
      )}
    </div>
  );
}

/** Over the whole box while files are dragged across it. */
function DropHint() {
  return (
    <div className="bg-card/85 text-muted-foreground pointer-events-none absolute inset-0 z-10 flex items-center justify-center gap-2 rounded-2xl text-sm">
      <PaperclipIcon className="size-4" />
      Drop files to attach
    </div>
  );
}
