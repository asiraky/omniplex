import { PaperclipIcon } from "lucide-react";
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
  type Ref,
} from "react";

import { ContextMeter } from "~/components/ContextMeter";
import type { Attachment } from "~/lib/attachments";
import type { ArtefactRef } from "~/lib/artefacts";
import type { PickerFocus } from "~/components/ModelPicker";
import { isComposing } from "~/lib/ime";
import { currentPlatform, formatShortcut } from "~/lib/keybindings";
import type { PickerInstance } from "~/lib/models";
import { cn } from "~/lib/utils";
import type { Delivery, HarnessMeta, Usage } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

import { AttachButton } from "./composer/AttachButton";
import { AttachmentStrip } from "./composer/AttachmentStrip";
import { CommandMenu } from "./composer/CommandMenu";
import { ComposerModelPicker } from "./composer/ComposerModelPicker";
import { DELIVERY_LABEL, deliveryFor, offersChoice } from "./composer/delivery";
import { SendButtons } from "./composer/SendButtons";
import { SendFailure } from "./composer/SendFailure";
import { UploadWait } from "./composer/UploadWait";
import { NO_CATALOGUE, type ComposerCatalogue } from "./composer/useComposerItems";
import { useComposerMenu } from "./composer/useComposerMenu";
import { useComposerSend } from "./composer/useComposerSend";
import { useDraftRef } from "./composer/useDraftRef";
import { useFileDrop, usePasteIntake } from "./composer/useFileIntake";

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
  /** Opens the model picker; "effort" puts focus on the effort control. Already open: moves focus there. */
  openModelPicker: (focus?: PickerFocus) => void;
}

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
  /** Extra controls in the toolbar, right after the model picker. */
  tools?: ReactNode;
  /** A drawer under the box, for context that is usually right already. */
  footer?: ReactNode;
  /** Present when the server can transcribe (an ElevenLabs key is stored), and
      names the thread a transcript is for. The new-thread screen has no thread
      yet, so it passes an empty object. */
  dictation?: { thread?: string };
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
}: ComposerProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
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

  const menu = useComposerMenu({
    draft,
    draftRef,
    changeDraft,
    catalogue,
    textareaRef,
    disabled,
    sendDisabled,
    runClientAction,
    onRunComposerAction,
  });
  const { send, cannotSend, sendableAttachments, carriesFiles, tooMuch } = useComposerSend({
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
        />
      )}
      {tools}
    </>
  );
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
      openModelPicker: (focus: PickerFocus = "model") => {
        if (!hasPicker) return;
        // A fresh object each time, so asking again while open moves focus.
        setPickerFocus({ target: focus });
        setModelPickerOpen(true);
      },
    }),
    [draftRef, focusAt, hasPicker],
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
        if (e.key !== "Enter") return;
        // Shift+Enter is the newline; let the textarea handle it.
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
        className={cn(
          "bg-card focus-within:border-ring focus-within:ring-ring/50 relative z-10 rounded-2xl border shadow-lg transition-[color,box-shadow] focus-within:ring-[3px]",
          drop.dragging && "border-primary ring-primary/50 ring-[3px]",
        )}
        {...drop.handlers}
      >
        {drop.dragging && (
          <div className="bg-card/85 text-muted-foreground pointer-events-none absolute inset-0 z-10 flex items-center justify-center gap-2 rounded-2xl text-sm">
            <PaperclipIcon className="size-4" />
            Drop files to attach
          </div>
        )}
        {waiting && <UploadWait />}
        {failure && (
          <SendFailure
            reason={failure.reason}
            canRetry={!disabled && !sendDisabled && !waiting}
            onRetry={failure.onRetry}
            onDismiss={failure.onDismiss}
          />
        )}
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

        <div className="flex items-center gap-1 px-2.5 pb-2">
          <AttachButton show={!!onAttachFiles} disabled={disabled} onFiles={attach} />

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
            carriesFiles={carriesFiles}
            onSend={(delivery) => void send(delivery)}
            onSchedule={onSchedule}
            onCancel={onCancel}
          />
        </div>
      </div>
      {footer && (
        // A drawer pulled out from under the box: narrower than it, tucked
        // behind its bottom edge, so it reads as part of the composer.
        <div className="bg-muted/50 mx-2.5 -mt-3 rounded-b-xl border border-t-0 pt-3">
          {footer}
        </div>
      )}
    </div>
  );
}
