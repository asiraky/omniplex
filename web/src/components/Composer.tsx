import { ArrowUpIcon, ChevronDownIcon, ClockIcon, PaperclipIcon, PlusIcon, SquareIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type ReactNode, type Ref } from "react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import { ContextMeter } from "~/components/ContextMeter";
import { ModelPicker } from "~/components/ModelPicker";
import { Button } from "~/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandItem, CommandList } from "~/components/ui/command";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "~/components/ui/dropdown-menu";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "~/components/ui/sheet";
import { Spinner } from "~/components/ui/spinner";
import {
  detectComposerTrigger,
  rankComposerItems,
  replaceComposerTrigger,
  submittedComposerAction,
} from "~/lib/composerItems";
import { formatContextWindow, pickerInstances, resolveInstance, resolveModel, type PickerInstance } from "~/lib/models";
import { cn } from "~/lib/utils";
import { dragHasFiles, filesFrom, sendPayload, type Attachment } from "~/lib/attachments";
import type { ArtefactRef } from "~/lib/artefacts";
import type { ComposerItem, HarnessMeta, Usage } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

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
  attachments = [],
  onAttachImages,
  onRemoveAttachment,
  disabledPlaceholder,
  harnesses = [],
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
  loadComposerItems,
  onRunClientAction,
  onRunComposerAction,
  onCommandUsed,
  tools,
  footer,
}: {
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
  onAttachImages?: (files: File[]) => void;
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
  loadComposerItems?: () => Promise<ComposerItem[]>;
  onRunClientAction?: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
  /** Reports the leading `/token` of a submitted message, so the parent can
      remember which skills this user actually reaches for. */
  onCommandUsed?: (insertText: string) => void;
  /** Extra controls in the toolbar, right after the model picker. */
  tools?: ReactNode;
  /** A drawer under the box, for context that is usually right already. */
  footer?: ReactNode;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // There is no ⇧↵ worth advertising on a phone, so keep the hint to desktop.
  const isDesktop = useIsDesktop();

  // The running model's own reasoning levels, so the effort control offers what
  // this model accepts rather than a fixed set. Legacy picks report none, and
  // the control simply does not appear.
  const modelEfforts = useMemo(() => {
    const instances = pickerInstances(harnesses);
    const inst = resolveInstance(instances, instance, harness);
    return resolveModel(inst, model)?.efforts ?? [];
  }, [harnesses, instance, harness, model]);
  const contextLabel = formatContextWindow(usage?.contextWindow);
  const [providerItems, setProviderItems] = useState<ComposerItem[]>([]);
  const [loadingItems, setLoadingItems] = useState(false);
  const [catalogueReady, setCatalogueReady] = useState(!loadComposerItems);
  const loadSequence = useRef(0);
  const draftRef = useRef(draft);
  const [cursor, setCursor] = useState(draft.length);
  const [activeIndex, setActiveIndex] = useState(0);
  const [dismissedTrigger, setDismissedTrigger] = useState("");
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  const [composerFocused, setComposerFocused] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  // Dragging over a child fires dragleave on the parent, so a boolean set from
  // those two events flickers as the pointer crosses the textarea. Depth counts
  // enters against leaves instead, and only zero means the drag has gone.
  const dragDepth = useRef(0);
  const [dragging, setDragging] = useState(false);

  const uploading = attachments.some((a) => a.status === "uploading");
  const sendableAttachments = attachments.filter((a) => a.status === "ready" || a.status === "staged").length;
  const carriesFiles = attachments.some((a) => a.kind === "file" && a.status !== "error");
  const cannotSend = disabled || sendDisabled || uploading || (!draft.trim() && sendableAttachments === 0);

  const attach = useCallback(
    (files: File[]) => {
      if (disabled || files.length === 0) return;
      onAttachImages?.(files);
    },
    [disabled, onAttachImages],
  );

  const items = useMemo<ComposerItem[]>(() => {
    const clientItems: ComposerItem[] = [
      {
        id: "client:model",
        name: "model",
        description: "Switch response model for this thread",
        kind: "command",
        trigger: "/",
        insertText: "/model",
        origin: "built-in",
        behavior: "client-action",
        action: "model",
      },
    ];
    const claimed = new Set(clientItems.map((item) => `${item.trigger}\0${item.insertText}`));
    return [
      ...clientItems,
      ...providerItems.filter((item) => !claimed.has(`${item.trigger}\0${item.insertText}`)),
    ];
  }, [providerItems]);

  const reloadItems = useCallback(() => {
    if (!loadComposerItems) {
      setProviderItems([]);
      setCatalogueReady(true);
      return;
    }
    const sequence = ++loadSequence.current;
    setLoadingItems(true);
    loadComposerItems()
      .then((next) => {
        if (sequence === loadSequence.current) {
          setProviderItems(next);
          setCatalogueReady(true);
        }
      })
      .catch(() => {
        // Retain a previously successful catalogue. If the first request
        // failed, catalogueReady remains false and slash text is not sent as
        // a prompt while its behavior is unknown.
      })
      .finally(() => {
        if (sequence === loadSequence.current) setLoadingItems(false);
      });
  }, [loadComposerItems]);

  // The catalogue cannot load while the workspace is still being prepared: the
  // actor has no thread to ask, so the request fails and `catalogueReady`
  // stays false. Retry on the edge where sending becomes possible, or a slash
  // command written during the wait would be silently refused afterwards.
  const wasSendBlocked = useRef(sendDisabled);
  useEffect(() => {
    if (wasSendBlocked.current && !sendDisabled) reloadItems();
    wasSendBlocked.current = sendDisabled;
  }, [reloadItems, sendDisabled]);

  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);

  const changeDraft = useCallback(
    (next: string) => {
      draftRef.current = next;
      onDraftChange(next);
    },
    [onDraftChange],
  );

  useEffect(() => {
    reloadItems();
    return () => {
      loadSequence.current++;
    };
  }, [reloadItems]);

  // Grow with the content, up to a cap. Runs on mount too, so a restored draft
  // opens at the right height instead of a single collapsed row.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [draft]);

  const focusAt = useCallback((nextCursor: number) => {
    window.requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(nextCursor, nextCursor);
      setCursor(nextCursor);
    });
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      focusEnd: (nextCursor?: number) => focusAt(nextCursor ?? draftRef.current.length),
    }),
    [focusAt],
  );

  const trigger = useMemo(
    () => detectComposerTrigger(draft, cursor, items),
    [draft, cursor, items],
  );

  // Provider catalogues can change while a thread is open. Refresh at the
  // start of each completion interaction; native adapters remain authoritative
  // without making the core subscribe to provider-specific invalidations.
  useEffect(() => {
    if (trigger?.query === "") reloadItems();
  }, [reloadItems, trigger?.trigger]); // query deliberately omitted: once per trigger opening
  const triggerKey = trigger ? `${trigger.start}:${trigger.end}:${trigger.trigger}:${trigger.query}` : "";
  const matches = useMemo(
    () => (trigger ? rankComposerItems(items, trigger) : []),
    [items, trigger],
  );
  const menuOpen = Boolean(
    trigger && triggerKey !== dismissedTrigger && !disabled && composerFocused,
  );

  useEffect(() => setActiveIndex(0), [triggerKey]);

  const choose = useCallback(
    (item: ComposerItem) => {
      if (!trigger) return;
      if (item.behavior === "client-action" && item.action) {
        const next = replaceComposerTrigger(draft, trigger, "");
        changeDraft(next.value);
        setDismissedTrigger(triggerKey);
        if (item.action === "model") {
          // Let the command popover close before opening the picker. Focusing
          // the textarea here would immediately dismiss the newly opened picker.
          window.requestAnimationFrame(() => setModelPickerOpen(true));
        } else {
          onRunClientAction?.(item.action);
        }
        return;
      }
      if (item.behavior === "adapter-action" && item.action) {
        // An adapter action is a turn by another name: it goes to the same
        // thread the send button is waiting on, so it waits with it.
        if (sendDisabled) return;
        const next = replaceComposerTrigger(draft, trigger, "");
        setDismissedTrigger(triggerKey);
        // Keep the literal command in place if the provider rejects it; App
        // has already surfaced the error and the user can retry or edit it.
        const submittedDraft = draft;
        void onRunComposerAction?.(item.action, "", item.insertText)
          .then(() => {
            if (draftRef.current === submittedDraft) changeDraft(next.value);
          })
          .catch(() => {});
        return;
      }
      const next = replaceComposerTrigger(draft, trigger, `${item.insertText} `);
      changeDraft(next.value);
      setDismissedTrigger(triggerKey);
      focusAt(next.cursor);
    },
    [changeDraft, draft, focusAt, onRunClientAction, onRunComposerAction, sendDisabled, trigger, triggerKey],
  );

  const send = async () => {
    const t = draft.trim();
    // A message may be nothing but attachments: "what is this?" is often the
    // whole question, and the picture or the file is the rest of it.
    if ((!t && sendableAttachments === 0) || disabled || sendDisabled) return;
    // Sending now would send the message without the file still on its way up,
    // which is not what attaching it meant.
    if (uploading) return;
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
      if (intercepted.item.action === "model") {
        window.requestAnimationFrame(() => setModelPickerOpen(true));
      } else if (intercepted.item.action) {
        onRunClientAction?.(intercepted.item.action);
      }
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

  const menu = (
    <Command shouldFilter={false} className="bg-transparent">
      <CommandList className="max-h-[min(45dvh,18rem)]">
        <CommandEmpty>{loadingItems ? "Loading commands…" : "No matching command."}</CommandEmpty>
        <CommandGroup>
          {matches.map((item, index) => (
            <CommandItem
              key={item.id}
              value={item.id}
              aria-selected={index === activeIndex}
              className={index === activeIndex ? "bg-accent text-accent-foreground" : undefined}
              onMouseDown={(event) => event.preventDefault()}
              onMouseMove={() => setActiveIndex(index)}
              onSelect={() => choose(item)}
            >
              <span className="min-w-0 flex-1">
                <span className="font-medium">{item.insertText}</span>
                {item.argsHint && <span className="text-muted-foreground ml-1">{item.argsHint}</span>}
                {item.description && (
                  <span className="text-muted-foreground ml-2 text-xs">{item.description}</span>
                )}
              </span>
              {item.origin && (
                <span className="text-muted-foreground shrink-0 text-[11px]">[{item.origin}]</span>
              )}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </Command>
  );

  const textarea = (
    <textarea
      ref={textareaRef}
      rows={1}
      value={draft}
      disabled={disabled}
      aria-label="Message"
      placeholder={
        disabled || sendDisabled
          ? (disabledPlaceholder ?? "Thread closed")
          : isDesktop
            ? "Ask anything…  (↵ to send · ⇧↵ for newline)"
            : "Ask anything…"
      }
      onChange={(e) => {
        changeDraft(e.target.value);
        setCursor(e.target.selectionStart);
      }}
      onPaste={(e) => {
        // A screenshot on the clipboard is the fastest way to attach one, and
        // the reason the terminal habit transfers; a copied file comes the same
        // way. Text pastes are untouched.
        const files = filesFrom(e.clipboardData);
        if (files.length === 0) return;
        e.preventDefault();
        attach(files);
      }}
      onFocus={() => setComposerFocused(true)}
      onBlur={() => setComposerFocused(false)}
      onClick={(e) => setCursor(e.currentTarget.selectionStart)}
      onKeyUp={(e) => setCursor(e.currentTarget.selectionStart)}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (menuOpen) {
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            if (matches.length > 0) {
              const offset = e.key === "ArrowDown" ? 1 : -1;
              setActiveIndex((index) => (index + offset + matches.length) % matches.length);
            }
            return;
          }
          if (e.key === "Escape") {
            e.preventDefault();
            setDismissedTrigger(triggerKey);
            return;
          }
          if (e.key === "Enter" || e.key === "Tab") {
            e.preventDefault();
            if (matches.length > 0) {
              choose(matches[Math.min(activeIndex, matches.length - 1)]!);
            }
            return;
          }
        }
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

  // What is going out with the next message: pictures as thumbnails, anything
  // else as a file tile. Sized for a thumb: the remove button is always
  // visible, because there is no hover on a phone.
  const strip = attachments.length > 0 && (
    <div className="flex flex-wrap gap-2 px-3 pt-3">
      {attachments.map((a) => (
        <div key={a.key} className={cn("relative", a.kind === "file" && "max-w-60 min-w-0")}>
          {a.kind === "file" ? (
            <FileChip attachment={a} />
          ) : (
            <>
              <img
                src={a.previewUrl}
                alt={a.name}
                className={cn("size-16 rounded-lg border object-cover", a.status === "error" && "opacity-40")}
              />
              {a.status === "uploading" && (
                <span className="bg-background/60 absolute inset-0 grid place-items-center rounded-lg">
                  <Spinner className="size-5" />
                </span>
              )}
              {a.status === "error" && (
                <span
                  title={a.error}
                  className="text-destructive absolute inset-0 grid place-items-center rounded-lg px-1 text-center text-[10px] leading-tight"
                >
                  {a.error ?? "Upload failed"}
                </span>
              )}
            </>
          )}
          <button
            type="button"
            onClick={() => onRemoveAttachment?.(a.key)}
            aria-label={`Remove ${a.name}`}
            className="bg-background text-muted-foreground hover:text-foreground absolute -top-2 -right-2 grid size-6 place-items-center rounded-full border shadow-sm"
          >
            <XIcon className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );

  return (
    <div className="mx-auto max-w-3xl px-4 pb-[calc(0.875rem+env(safe-area-inset-bottom))] md:px-5">
      <div
        className={cn(
          "bg-card focus-within:border-ring focus-within:ring-ring/50 relative z-10 rounded-2xl border shadow-lg transition-[color,box-shadow] focus-within:ring-[3px]",
          dragging && "border-primary ring-primary/50 ring-[3px]",
        )}
        onDragEnter={(e) => {
          if (!dragHasFiles(e.dataTransfer)) return;
          e.preventDefault();
          dragDepth.current++;
          setDragging(true);
        }}
        onDragOver={(e) => {
          if (dragHasFiles(e.dataTransfer)) e.preventDefault();
        }}
        onDragLeave={() => {
          if (dragDepth.current > 0) dragDepth.current--;
          if (dragDepth.current === 0) setDragging(false);
        }}
        onDrop={(e) => {
          if (!dragHasFiles(e.dataTransfer)) return;
          e.preventDefault();
          dragDepth.current = 0;
          setDragging(false);
          attach(filesFrom(e.dataTransfer));
        }}
      >
        {dragging && (
          <div className="bg-card/85 text-muted-foreground pointer-events-none absolute inset-0 z-10 flex items-center justify-center gap-2 rounded-2xl text-sm">
            <PaperclipIcon className="size-4" />
            Drop files to attach
          </div>
        )}
        {strip}
        {isDesktop ? (
          <Popover open={menuOpen}>
            <PopoverAnchor asChild>{textarea}</PopoverAnchor>
            <PopoverContent
              side="top"
              align="start"
              onOpenAutoFocus={(event) => event.preventDefault()}
              className="w-[min(40rem,calc(100vw-2rem))] p-0"
            >
              {menu}
            </PopoverContent>
          </Popover>
        ) : (
          <>
            {textarea}
            <Sheet
              modal={false}
              open={menuOpen}
              onOpenChange={(open) => {
                if (!open) setDismissedTrigger(triggerKey);
              }}
            >
              <SheetContent
                side="bottom"
                onOpenAutoFocus={(event) => event.preventDefault()}
                className="max-h-[70dvh] p-0 pb-[env(safe-area-inset-bottom)]"
              >
                <SheetHeader><SheetTitle>Commands</SheetTitle></SheetHeader>
                {menu}
              </SheetContent>
            </Sheet>
          </>
        )}

        <div className="flex items-center gap-1 px-2.5 pb-2">
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => {
              attach(Array.from(e.target.files ?? []));
              // Cleared so picking the same file twice in a row still fires.
              e.target.value = "";
            }}
          />
          {onAttachImages && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              disabled={disabled}
              onClick={() => fileInputRef.current?.click()}
              aria-label="Attach files"
              title="Attach files"
              className="text-muted-foreground hover:text-foreground size-11 shrink-0 rounded-full md:size-8"
            >
              <PlusIcon />
            </Button>
          )}

          {harnesses.length > 0 && (
            // The one control for what runs the next turn: the model, the
            // account it bills to among this harness's own accounts, and
            // reasoning effort, which opens out of the same menu rather than
            // sitting beside it as a second dropdown.
            <ModelPicker
              harnesses={harnesses}
              lockDriver={!anyHarness}
              onInstanceChange={onPickInstance}
              disabled={disabled}
              efforts={onSwitchEffort ? modelEfforts : []}
              effort={effort}
              contextLabel={contextLabel}
              onEffortChange={onSwitchEffort}
              value={{ harness, instance, model }}
              onChange={(next) => {
                const current = resolveInstance(pickerInstances(harnesses), instance, harness)?.id;
                if (next.instance !== current) {
                  onSwitchAccount?.(next.instance, next.model);
                  return;
                }
                onSwitchModel?.(next.model);
                // Effort is per model: a level the old model allowed (Codex's
                // "ultra") may be one the new model rejects, which would break
                // its next turn. When the chosen model does not support the
                // current effort, drop to its strongest supported level —
                // closest to the intent, and a valid, displayable value.
                const nextEfforts =
                  resolveModel(resolveInstance(pickerInstances(harnesses), instance, harness), next.model)
                    ?.efforts ?? [];
                if (effort && nextEfforts.length > 0 && !nextEfforts.includes(effort)) {
                  onSwitchEffort?.(nextEfforts[nextEfforts.length - 1]);
                }
              }}
              open={modelPickerOpen}
              onOpenChange={setModelPickerOpen}
              compact
              // shrink undoes Button's shrink-0: the picker is the one control
              // in this row that can give up width, so it must, or the send
              // button is what gets pushed off a narrow screen.
              className="text-muted-foreground hover:text-foreground hover:bg-accent dark:hover:bg-accent h-11 w-auto max-w-[55%] min-w-0 shrink border-0 bg-transparent px-2 shadow-none dark:bg-transparent md:h-8 md:min-h-8"
            />
          )}

          {tools}

          <span className="flex-1" />

          {usage && (usage.contextUsed ?? 0) > 0 && <ContextMeter usage={usage} model={model} />}

          {busy && (
            <Button
              variant="destructive"
              size="icon"
              onClick={onCancel}
              aria-label="Interrupt the running turn"
              title="Interrupt the running turn"
              // Set apart from the model control beside it: the send button
              // ends the row rather than continuing it, and a shared gap made
              // the two read as one cluster.
              className="ml-1.5 size-11 shrink-0 rounded-full md:ml-2 md:size-8"
            >
              <SquareIcon className="size-3.5 fill-current" />
            </Button>
          )}
          {/* Sending while a turn runs hands the message to the harness,
              which reads it at its next step. The button only appears once
              there is something to send, so an idle-looking stop button is
              not crowded by a dead send. */}
          {(!busy || draft.trim() || sendableAttachments > 0) && (
            // Scheduling rides on send's edge rather than taking its own slot:
            // a phone-width row has no room for a third round button.
            <div className="ml-1.5 flex shrink-0 md:ml-2">
              <Button
                size="icon"
                disabled={cannotSend}
                onClick={() => void send()}
                aria-label={busy ? "Send to the running turn" : "Send"}
                title={busy ? "The model reads it after its current step" : undefined}
                className={cn("size-11 shrink-0 rounded-full md:size-8", onSchedule && "rounded-r-none")}
              >
                <ArrowUpIcon />
              </Button>
              {onSchedule && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      size="icon"
                      disabled={cannotSend}
                      aria-label="More send options"
                      className="border-primary-foreground/25 h-11 w-7 shrink-0 rounded-l-none rounded-r-full border-l md:h-8 md:w-6"
                    >
                      <ChevronDownIcon className="size-3.5" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent side="top" align="end">
                    <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={() => void send()}>
                      <ArrowUpIcon />
                      Send now
                    </DropdownMenuItem>
                    {/* A scheduled prompt keeps its text and images, not files. */}
                    <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={onSchedule} disabled={carriesFiles}>
                      <ClockIcon />
                      <span className="flex flex-col">
                        Schedule send…
                        {carriesFiles && <span className="text-muted-foreground text-xs">Not with files attached</span>}
                      </span>
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
          )}
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

/** A staged non-image file: its tile, with the upload's progress or failure
    in place of its size until it is ready. */
function FileChip({ attachment: a }: { attachment: Attachment }) {
  const pct = Math.round((a.progress ?? 0) * 100);
  const detail =
    a.status === "uploading" ? (
      `Uploading ${pct}%`
    ) : a.status === "error" ? (
      <span className="text-destructive" title={a.error}>
        {a.error ?? "Upload failed"}
      </span>
    ) : undefined;
  return (
    <div className="relative">
      <ArtefactTile
        compact
        name={a.name}
        mediaType={a.mediaType ?? "application/octet-stream"}
        size={a.size ?? 0}
        detail={detail}
        className={cn("pr-4", a.status === "error" && "border-destructive/50")}
      />
      {a.status === "uploading" && (
        <span
          role="progressbar"
          aria-label={`Uploading ${a.name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          className="absolute inset-x-2 bottom-0.5 h-0.5 overflow-hidden rounded-full"
        >
          <span className="bg-primary block h-full transition-[width]" style={{ width: `${pct}%` }} />
        </span>
      )}
    </div>
  );
}
