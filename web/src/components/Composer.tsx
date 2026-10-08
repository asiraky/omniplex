import { PaperclipIcon } from "lucide-react";
import { useCallback, useEffect, useImperativeHandle, useRef, useState, type ReactNode, type Ref } from "react";

import { ContextMeter } from "~/components/ContextMeter";
import type { Attachment } from "~/lib/attachments";
import type { ArtefactRef } from "~/lib/artefacts";
import type { PickerInstance } from "~/lib/models";
import { cn } from "~/lib/utils";
import type { HarnessMeta, Usage } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

import { AttachButton } from "./composer/AttachButton";
import { AttachmentStrip } from "./composer/AttachmentStrip";
import { CommandMenu } from "./composer/CommandMenu";
import { ComposerModelPicker } from "./composer/ComposerModelPicker";
import { SendButtons } from "./composer/SendButtons";
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

/** What the transcript's recent-skills list needs from the composer. */
export interface ComposerHandle {
  /** Focuses the input and parks the cursor at `cursor`, or at the end. */
  focusEnd: (cursor?: number) => void;
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
  onSend: (text: string, attached: ComposerAttachments) => void;
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
  disabledPlaceholder?: string;
  /** Every harness the server reports; the picker reads this thread's out. */
  harnesses?: HarnessMeta[];
  /** The attached thread's harness, which it cannot change, and account,
      which it can — to another account of the same harness. */
  harness?: string;
  instance?: string;
  model?: string;
  effort?: string;
  onSwitchModel?: (id: string) => void;
  onSwitchEffort?: (effort: string) => void;
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
  /** Extra controls in the toolbar, right after the model picker. */
  tools?: ReactNode;
  /** A drawer under the box, for context that is usually right already. */
  footer?: ReactNode;
}

const NO_HARNESSES: HarnessMeta[] = [];
const NO_ATTACHMENTS: Attachment[] = [];

function placeholderFor(blocked: boolean, disabledPlaceholder: string | undefined, isDesktop: boolean) {
  if (blocked) return disabledPlaceholder ?? "Thread closed";
  // There is no ⇧↵ worth advertising on a phone, so keep the hint to desktop.
  return isDesktop ? "Ask anything…  (↵ to send · ⇧↵ for newline)" : "Ask anything…";
}

export function Composer({
  ref,
  draft,
  onDraftChange,
  disabled,
  sendDisabled = false,
  busy,
  onSend,
  onSchedule,
  onCancel,
  attachments = NO_ATTACHMENTS,
  onAttachFiles,
  onRemoveAttachment,
  disabledPlaceholder,
  harnesses = NO_HARNESSES,
  harness = "",
  instance = "",
  model = "",
  effort = "",
  onSwitchModel,
  onSwitchEffort,
  onSwitchAccount,
  anyHarness = false,
  onPickInstance,
  usage,
  catalogue = NO_CATALOGUE,
  onRunClientAction,
  onRunComposerAction,
  onCommandUsed,
  tools,
  footer,
}: ComposerProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const isDesktop = useIsDesktop();
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
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
    catalogueReady: catalogue.ready,
    items: menu.items,
    onSend,
    onCommandUsed,
    runClientAction,
    onRunComposerAction,
  });

  // Grow with the content, up to a cap. Runs on mount too, so a restored draft
  // opens at the right height instead of a single collapsed row.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [draft]);

  const { focusAt } = menu;
  useImperativeHandle(
    ref,
    () => ({
      focusEnd: (nextCursor?: number) => focusAt(nextCursor ?? draftRef.current.length),
    }),
    [draftRef, focusAt],
  );

  const textarea = (
    <textarea
      ref={textareaRef}
      rows={1}
      value={draft}
      disabled={disabled}
      aria-label="Message"
      placeholder={placeholderFor(disabled || sendDisabled, disabledPlaceholder, isDesktop)}
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
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (menu.onKey(e)) return;
        if (e.key !== "Enter") return;
        // Shift+Enter is the newline; let the textarea handle it.
        if (e.shiftKey) return;
        e.preventDefault();
        void send();
      }}
      // 16px on a phone: anything smaller makes iOS zoom the viewport on
      // focus, which breaks the layout the dvh handling just fixed.
      className="scroll-thin placeholder:text-muted-foreground max-h-[200px] min-h-16 w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-[16px] leading-relaxed focus:outline-none disabled:opacity-60 md:text-[14px]"
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
        <AttachmentStrip attachments={attachments} tooMuch={tooMuch} onRemove={onRemoveAttachment} />
        <CommandMenu
          open={menu.open}
          anchor={textarea}
          matches={menu.matches}
          activeIndex={menu.activeIndex}
          loading={catalogue.loading}
          onHover={menu.setActiveIndex}
          onChoose={menu.choose}
        />

        <div className="flex items-center gap-1 px-2.5 pb-2">
          <AttachButton show={!!onAttachFiles} disabled={disabled} onFiles={attach} />

          {harnesses.length > 0 && (
            <ComposerModelPicker
              harnesses={harnesses}
              harness={harness}
              instance={instance}
              model={model}
              effort={effort}
              contextWindow={usage?.contextWindow}
              anyHarness={anyHarness}
              disabled={disabled}
              open={modelPickerOpen}
              onOpenChange={setModelPickerOpen}
              onSwitchModel={onSwitchModel}
              onSwitchEffort={onSwitchEffort}
              onSwitchAccount={onSwitchAccount}
              onPickInstance={onPickInstance}
            />
          )}

          {tools}

          <span className="flex-1" />

          {usage && (usage.contextUsed ?? 0) > 0 && <ContextMeter usage={usage} model={model} />}

          <SendButtons
            busy={busy}
            hasContent={!!draft.trim() || sendableAttachments > 0}
            cannotSend={cannotSend}
            carriesFiles={carriesFiles}
            onSend={() => void send()}
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
