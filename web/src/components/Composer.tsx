import { ArrowUpIcon, ImageIcon, PlusIcon, SquareIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";

import { ContextMeter } from "~/components/ContextMeter";
import { ModelPicker } from "~/components/ModelPicker";
import { Button } from "~/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandItem, CommandList } from "~/components/ui/command";
import { Popover, PopoverAnchor, PopoverContent } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "~/components/ui/sheet";
import { Spinner } from "~/components/ui/spinner";
import {
  detectComposerTrigger,
  rankComposerItems,
  replaceComposerTrigger,
  submittedComposerAction,
} from "~/lib/composerItems";
import { formatContextWindow, pickerInstances, resolveInstance, resolveModel } from "~/lib/models";
import { cn } from "~/lib/utils";
import { dragHasFiles, imageFilesFrom, IMAGE_ACCEPT, type Attachment } from "~/lib/attachments";
import type { ComposerItem, HarnessMeta, Usage } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

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
  usage,
  loadComposerItems,
  onRunClientAction,
  onRunComposerAction,
  onCommandUsed,
}: {
  ref?: Ref<ComposerHandle>;
  /**
   * The in-progress message. Owned by the parent and keyed per session there,
   * so it survives this component being unmounted and remounted across a
   * session switch — the draft is not this component's to lose.
   */
  draft: string;
  onDraftChange: (text: string) => void;
  disabled: boolean;
  /** Blocks sending without locking the input. The workspace being prepared is
      not a reason to stop someone writing the first message — only a reason to
      hold it back until there is something to send it to. */
  sendDisabled?: boolean;
  busy: boolean;
  onSend: (text: string) => void;
  onCancel: () => void;
  /** Images staged for the next message. Owned by the parent for the same
      reason the draft is: a session switch unmounts this component. */
  attachments?: Attachment[];
  /** Hands picked, dropped, or pasted images to the parent, which uploads
      them. Anything that is not a file is left to the textarea. */
  onAttachImages?: (files: File[]) => void;
  onRemoveAttachment?: (key: string) => void;
  disabledPlaceholder?: string;
  /** Every harness the server reports; the picker reads this session's out. */
  harnesses?: HarnessMeta[];
  /** The attached session's harness and account, which it cannot change. */
  harness?: string;
  instance?: string;
  model?: string;
  effort?: string;
  onSwitchModel?: (id: string) => void;
  onSwitchEffort?: (effort: string) => void;
  /** The session's token usage, source of the context meter. */
  usage?: Usage;
  loadComposerItems?: () => Promise<ComposerItem[]>;
  onRunClientAction?: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
  /** Reports the leading `/token` of a submitted message, so the parent can
      remember which skills this user actually reaches for. */
  onCommandUsed?: (insertText: string) => void;
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
  const sendableImages = attachments.filter((a) => a.status === "ready").length;

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
        description: "Switch response model for this session",
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
  // actor has no session to ask, so the request fails and `catalogueReady`
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

  // Provider catalogues can change while a session is open. Refresh at the
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
        // session the send button is waiting on, so it waits with it.
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
    // A message may be nothing but pictures: "what is this?" is often the whole
    // question, and the picture is the rest of it.
    if ((!t && sendableImages === 0) || disabled || sendDisabled) return;
    // Sending now would send the message without the image still on its way up,
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
    onSend(t);
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
          ? (disabledPlaceholder ?? "Session closed")
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
        // the reason the terminal habit transfers. Text pastes are untouched.
        const files = imageFilesFrom(e.clipboardData);
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
      className="scroll-thin placeholder:text-muted-foreground max-h-[200px] w-full resize-none bg-transparent px-4 pt-3 pb-1 text-[16px] leading-relaxed focus:outline-none disabled:opacity-60 md:text-[14px]"
    />
  );

  // Thumbnails of what is going out with the next message. Sized for a thumb:
  // the remove button is always visible, because there is no hover on a phone.
  const strip = attachments.length > 0 && (
    <div className="flex flex-wrap gap-2 px-3 pt-3">
      {attachments.map((a) => (
        <div key={a.key} className="relative">
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
          "bg-card focus-within:border-ring focus-within:ring-ring/50 relative rounded-2xl border shadow-lg transition-[color,box-shadow] focus-within:ring-[3px]",
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
          attach(imageFilesFrom(e.dataTransfer));
        }}
      >
        {dragging && (
          <div className="bg-card/85 text-muted-foreground pointer-events-none absolute inset-0 z-10 flex items-center justify-center gap-2 rounded-2xl text-sm">
            <ImageIcon className="size-4" />
            Drop images to attach
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
            accept={IMAGE_ACCEPT}
            multiple
            className="hidden"
            onChange={(e) => {
              attach(Array.from(e.target.files ?? []));
              // Cleared so picking the same file twice in a row still fires.
              e.target.value = "";
            }}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={disabled}
            onClick={() => fileInputRef.current?.click()}
            aria-label="Attach images"
            title="Attach images"
            className="text-muted-foreground hover:text-foreground size-11 shrink-0 rounded-full md:size-8"
          >
            <PlusIcon />
          </Button>

          {usage && (usage.contextUsed ?? 0) > 0 && <ContextMeter usage={usage} model={model} />}

          <span className="flex-1" />

          {harnesses.length > 0 && (
            // The one control for what runs the next turn: the account is
            // fixed — the harness is already running under it — so the model
            // is the choice, and reasoning effort opens out of the same menu
            // rather than sitting beside it as a second dropdown.
            <ModelPicker
              harnesses={harnesses}
              lockInstance
              disabled={disabled}
              efforts={onSwitchEffort ? modelEfforts : []}
              effort={effort}
              contextLabel={contextLabel}
              onEffortChange={onSwitchEffort}
              value={{ harness, instance, model }}
              onChange={(next) => {
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
              className="text-muted-foreground hover:text-foreground h-11 w-auto max-w-[55%] min-w-0 border-0 px-2 shadow-none md:h-8 md:min-h-8"
            />
          )}

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
          {(!busy || draft.trim() || sendableImages > 0) && (
            <Button
              size="icon"
              disabled={disabled || sendDisabled || uploading || (!draft.trim() && sendableImages === 0)}
              onClick={() => void send()}
              aria-label={busy ? "Send to the running turn" : "Send"}
              title={busy ? "The model reads it after its current step" : undefined}
              className="ml-1.5 size-11 shrink-0 rounded-full md:ml-2 md:size-8"
            >
              <ArrowUpIcon />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
