import {
  ClockIcon,
  ArrowUpIcon,
  ChevronUpIcon,
  FileTextIcon,
  ImageIcon,
  PlusIcon,
  ShieldIcon,
  SquareIcon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";

import { ComposerChip } from "~/components/ComposerChip";
import { ComposerMirror } from "~/components/ComposerMirror";
import { ContextMeter } from "~/components/ContextMeter";
import { ModelPicker } from "~/components/ModelPicker";
import type { QuickViewTarget } from "~/components/QuickView";
import { Button } from "~/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandItem, CommandList } from "~/components/ui/command";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
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
import {
  blobLabel,
  blobPeek,
  caretOffsetFromPoint,
  dragHasRef,
  insertRef,
  isBlobWorthy,
  parseRefs,
  refAt,
  refFromDrag,
  removeRef,
  type Blob,
  type BlobOrigin,
  type FileRef,
  type RefMatch,
} from "~/lib/composerRefs";
import { originOf } from "~/lib/copyOrigin";
import { fileIconFor } from "~/lib/fileIcons";
import { DELIVERY_OPTIONS, DEFAULT_DELIVERY, deliveryLabel, loadDelivery, saveDelivery } from "~/lib/delivery";
import type { ComposerItem, Delivery, HarnessMeta, PermissionModeMeta, Usage } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

/** What the transcript's recent-skills list needs from the composer. */
export interface ComposerHandle {
  /** Focuses the input and parks the cursor at `cursor`, or at the end. */
  focusEnd: (cursor?: number) => void;
  /** Writes a `@path` chip into the draft at the caret. The panel's way of
      adding a file where dragging one is not possible — which is every touch
      screen, the panel being a full-screen sheet there. */
  addRef: (ref: FileRef) => void;
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
  blobs = [],
  onAttachBlob,
  onRemoveBlob,
  onQuickView,
  disabledPlaceholder,
  harnesses = [],
  harness = "",
  instance = "",
  model = "",
  effort = "",
  onSwitchModel,
  onSwitchEffort,
  sessionId = "",
  modes = [],
  mode = "",
  onSwitchMode,
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
  /** `delivery` is only meaningful while a turn runs; idle sends pass `now`. */
  onSend: (text: string, delivery: Delivery) => void;
  onSchedule?: () => void;
  onCancel: () => void;
  /** Images staged for the next message. Owned by the parent for the same
      reason the draft is: a session switch unmounts this component. */
  attachments?: Attachment[];
  /** Hands picked, dropped, or pasted images to the parent, which uploads
      them. Anything that is not a file is left to the textarea. */
  onAttachImages?: (files: File[]) => void;
  onRemoveAttachment?: (key: string) => void;
  /** Large pastes held out of the draft, owned by the parent for the same
      reason the draft and the images are. */
  blobs?: Blob[];
  onAttachBlob?: (text: string, origin: BlobOrigin) => void;
  onRemoveBlob?: (key: string) => void;
  /** Opens a chip's contents. The dialog lives in the parent so it outlives a
      composer remount across a session switch. */
  onQuickView?: (target: QuickViewTarget) => void;
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
  /** Scopes the remembered delivery choice; empty means do not remember. */
  sessionId?: string;
  /** The harness's permission modes, and the one this session runs in. Changing
      it takes effect on the next tool call — no restart — so it belongs beside
      the model, in reach of a thumb, rather than in the header. */
  modes?: PermissionModeMeta[];
  mode?: string;
  onSwitchMode?: (id: string) => void;
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
  // An unrecognised or empty recorded mode means "the harness default"; render
  // it as that rather than as nothing.
  const currentMode = modes.find((m) => m.id === mode) ?? modes.find((m) => m.default) ?? modes[0];
  const [providerItems, setProviderItems] = useState<ComposerItem[]>([]);
  const [loadingItems, setLoadingItems] = useState(false);
  const [catalogueReady, setCatalogueReady] = useState(!loadComposerItems);
  const loadSequence = useRef(0);
  const draftRef = useRef(draft);
  const [cursor, setCursor] = useState(draft.length);
  const [activeIndex, setActiveIndex] = useState(0);
  const [dismissedTrigger, setDismissedTrigger] = useState("");
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  // Sticky per session: picking "interrupt" once usually means the next few
  // messages are the same kind of message. The composer is keyed by session id
  // in App, so the initialiser runs once per session and needs no effect.
  const [delivery, setDelivery] = useState<Delivery>(() => loadDelivery(sessionId));
  const [deliveryMenuOpen, setDeliveryMenuOpen] = useState(false);
  const longPress = useRef(0);
  const longPressed = useRef(false);
  const cancelLongPress = useCallback(() => {
    if (longPress.current) window.clearTimeout(longPress.current);
    longPress.current = 0;
  }, []);
  useEffect(() => cancelLongPress, [cancelLongPress]);
  const chooseDelivery = useCallback(
    (next: Delivery) => {
      setDelivery(next);
      saveDelivery(sessionId, next);
    },
    [sessionId],
  );
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

  // ---- file references ----

  /** Write a `@path` token in, at `at` or wherever the caret is. */
  const insertRefAt = useCallback(
    (fileRef: FileRef, at?: number) => {
      const el = textareaRef.current;
      const current = draftRef.current;
      const next = insertRef(current, at ?? el?.selectionStart ?? current.length, fileRef);
      changeDraft(next.value);
      focusAt(next.cursor);
    },
    [changeDraft, focusAt],
  );

  const takeRefOut = useCallback(
    (match: RefMatch) => {
      const next = removeRef(draftRef.current, match);
      changeDraft(next.value);
      focusAt(next.cursor);
    },
    [changeDraft, focusAt],
  );

  // On a phone the tokens are also listed as chips above the box: there is no
  // pointer to hover a pill with and no comfortable way to pick one out of a
  // sentence, so the strip is where a reference gets previewed and removed.
  // Parsing is skipped entirely on a desktop, where the pills do that job.
  const refChips = useMemo(() => (isDesktop ? [] : parseRefs(draft)), [isDesktop, draft]);

  // Whether the pill overlay is worth mounting at all. Scanning for "@" is a
  // fast reject on the overwhelmingly common draft that has no references, and
  // keeps a plain message from paying for a second layout box per keystroke.
  const showMirror = isDesktop && draft.includes("@");

  // The positioned box the mirror measures itself against.
  const cardRef = useRef<HTMLDivElement>(null);
  // A file row being dragged in, as opposed to image files. Kept apart from
  // `dragging` because the two want different words on the overlay.
  const [refDragging, setRefDragging] = useState(false);

  useImperativeHandle(
    ref,
    () => ({
      focusEnd: (nextCursor?: number) => focusAt(nextCursor ?? draftRef.current.length),
      addRef: (fileRef: FileRef) => insertRefAt(fileRef),
    }),
    [focusAt, insertRefAt],
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
    if ((!t && sendableImages === 0 && blobs.length === 0) || disabled || sendDisabled) return;
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
    // An idle session has nothing to interrupt and nothing to wait for, so the
    // choice does not travel: it would only park the message behind a turn
    // that is not running.
    onSend(t, busy ? delivery : DEFAULT_DELIVERY);
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
        // the reason the terminal habit transfers.
        const files = imageFilesFrom(e.clipboardData);
        if (files.length > 0) {
          e.preventDefault();
          attach(files);
          return;
        }
        // A stack trace or a log tail pasted in full turns the composer into a
        // wall of text with the actual question lost somewhere inside it.
        // Anything that big collapses to a chip instead, tagged with where it
        // was copied from when it was copied from inside this app.
        if (!onAttachBlob || disabled) return;
        const text = e.clipboardData.getData("text/plain");
        if (!isBlobWorthy(text)) return;
        e.preventDefault();
        onAttachBlob(text, originOf(text));
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
        // A chip reads as one thing, so it deletes as one thing: backspace
        // with the caret against the end of a `@path` token takes the whole
        // token rather than the last character of a path, which would leave a
        // half-token still painted as a pill.
        if (e.key === "Backspace" && !e.shiftKey && !e.metaKey && !e.altKey) {
          const el = e.currentTarget;
          if (el.selectionStart !== el.selectionEnd) return;
          const match = refAt(draft, el.selectionStart);
          if (!match || match.end !== el.selectionStart) return;
          e.preventDefault();
          takeRefOut(match);
          return;
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

  // Everything going out with the next message that is not prose: pictures as
  // thumbnails, collapsed pastes as chips, and — on a phone only — the file
  // references a desktop paints inside the text instead. Sized for a thumb:
  // the remove button is always visible, because there is no hover on a phone.
  const strip = (attachments.length > 0 || blobs.length > 0 || refChips.length > 0) && (
    <div className="flex flex-wrap items-center gap-2 px-3 pt-3">
      {refChips.map((match) => {
        const { Icon, tone } = fileIconFor(match.path);
        const name = match.path.slice(match.path.lastIndexOf("/") + 1);
        return (
          <ComposerChip
            key={`${match.start}:${match.text}`}
            icon={<Icon className={cn("size-3.5", tone || "text-muted-foreground/70")} />}
            label={match.from ? `${name}:${match.from}` : name}
            sub={match.path}
            onOpen={
              onQuickView && (() => onQuickView({ kind: "file", path: match.path, line: match.from }))
            }
            onRemove={() => takeRefOut(match)}
            removeLabel={`Remove ${match.path}`}
          />
        );
      })}
      {blobs.map((blob) => (
        <ComposerChip
          key={blob.key}
          icon={<FileTextIcon className="text-muted-foreground/70 size-3.5" />}
          label={blobLabel(blob)}
          sub={blobPeek(blob)}
          onOpen={onQuickView && (() => onQuickView({ kind: "blob", blob }))}
          onRemove={() => onRemoveBlob?.(blob.key)}
          removeLabel={`Remove ${blobLabel(blob)}`}
        />
      ))}
      {attachments.map((a) => (
        <div key={a.key} className="relative">
          <img
            src={a.previewUrl}
            alt={a.name}
            // A finished picture opens full size; one still uploading has
            // nothing bigger to show yet, and a failed one has nothing at all.
            onClick={
              a.status === "ready" && onQuickView
                ? () => onQuickView({ kind: "image", src: a.previewUrl, name: a.name })
                : undefined
            }
            className={cn(
              "size-16 rounded-lg border object-cover",
              a.status === "error" && "opacity-40",
              a.status === "ready" && onQuickView && "cursor-pointer",
            )}
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
        ref={cardRef}
        className={cn(
          "bg-card focus-within:border-ring focus-within:ring-ring/50 relative rounded-2xl border shadow-lg transition-[color,box-shadow] focus-within:ring-[3px]",
          (dragging || refDragging) && "border-primary ring-primary/50 ring-[3px]",
        )}
        onDragEnter={(e) => {
          const ref = dragHasRef(e.dataTransfer);
          if (!ref && !dragHasFiles(e.dataTransfer)) return;
          e.preventDefault();
          dragDepth.current++;
          if (ref) setRefDragging(true);
          else setDragging(true);
        }}
        onDragOver={(e) => {
          // A file row is copied in, not moved out of the tree; saying so is
          // what gets the cursor to show a plus instead of a "no entry".
          if (dragHasRef(e.dataTransfer)) {
            e.preventDefault();
            e.dataTransfer.dropEffect = "copy";
            return;
          }
          if (dragHasFiles(e.dataTransfer)) e.preventDefault();
        }}
        onDragLeave={() => {
          if (dragDepth.current > 0) dragDepth.current--;
          if (dragDepth.current === 0) {
            setDragging(false);
            setRefDragging(false);
          }
        }}
        onDrop={(e) => {
          const fileRef = refFromDrag(e.dataTransfer);
          if (fileRef) {
            e.preventDefault();
            dragDepth.current = 0;
            setRefDragging(false);
            // Where the pointer let go, so a chip can land between two words
            // rather than always at the end. The browsers that will not
            // answer get the end of the draft, which is where a drop on the
            // card's padding belongs anyway.
            const el = textareaRef.current;
            const at =
              el && el.contains(e.target as Node)
                ? caretOffsetFromPoint(el, e.clientX, e.clientY)
                : null;
            insertRefAt(fileRef, at ?? undefined);
            return;
          }
          if (!dragHasFiles(e.dataTransfer)) return;
          e.preventDefault();
          dragDepth.current = 0;
          setDragging(false);
          attach(imageFilesFrom(e.dataTransfer));
        }}
      >
        {dragging && (
          <div className="bg-card/85 text-muted-foreground pointer-events-none absolute inset-0 z-20 flex items-center justify-center gap-2 rounded-2xl text-sm">
            <ImageIcon className="size-4" />
            Drop images to attach
          </div>
        )}
        {/* A file drop lands at the pointer, so unlike an image drop it must
            not black out the text being aimed at: the hint sits in the corner
            and the draft — and the drop caret the browser draws in it — stays
            readable underneath. */}
        {refDragging && (
          <div className="bg-primary/10 text-primary pointer-events-none absolute -top-2.5 right-3 z-20 flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] backdrop-blur-sm">
            <FileTextIcon className="size-3" />
            Drop to mention
          </div>
        )}
        {strip}
        {showMirror && (
          <ComposerMirror
            textareaRef={textareaRef}
            anchorRef={cardRef}
            text={draft}
            onRemove={takeRefOut}
          />
        )}
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

          {/* One group between the attach button and the send cluster, and the
              group is what gives way when the row runs out of phone. Send and
              stop must never be the controls pushed off the screen edge, so
              they stay put while the model name and the mode label truncate. */}
          <div className="flex min-w-0 flex-1 items-center justify-end gap-1">
            {usage && (usage.contextUsed ?? 0) > 0 && <ContextMeter usage={usage} model={model} />}

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
                compactTrigger
                // No width cap of its own any more: the group it sits in is the
                // cap, and inside it the picker and the mode chip shrink
                // together rather than the picker hitting a percentage of the
                // whole row while space beside it goes unused. While a turn
                // runs, a phone row also carries stop and the delivery chevron,
                // and there is no width left for a legible model name: it steps
                // out until the turn ends rather than shrinking to a lone
                // chevron on top of the mode chip.
                className={cn(
                  "text-muted-foreground hover:text-foreground h-11 w-auto min-w-0 shrink border-0 px-2 shadow-none md:h-8 md:min-h-8",
                  busy && "hidden md:inline-flex",
                )}
              />
            )}

            {modes.length > 0 && onSwitchMode && (
              // A phone row has no width for a third label — the model name and
              // the send cluster are already spending it — so below md the chip
              // is the shield alone, brightened when the session is in anything
              // but the harness's default. That is the thing you need to catch
              // at a glance; which mode it is, is one tap and a ✓ away, and the
              // label itself returns as soon as the screen can hold it.
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={disabled}
                    aria-label={`Permission mode: ${currentMode?.label ?? "default"}`}
                    title={`Permission mode: ${currentMode?.label ?? "default"}`}
                    className={cn(
                      "hover:text-foreground h-11 min-w-0 shrink gap-1 rounded-full px-2 text-[11px] md:h-8",
                      currentMode && !currentMode.default ? "text-foreground" : "text-muted-foreground",
                    )}
                  >
                    <ShieldIcon className="size-3.5 shrink-0" />
                    {currentMode && !currentMode.default && (
                      <span className="hidden max-w-24 truncate md:inline">{currentMode.label}</span>
                    )}
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="max-w-[min(22rem,calc(100vw-2rem))]">
                  <DropdownMenuLabel>Permission mode</DropdownMenuLabel>
                  {modes.map((m) => (
                    <DropdownMenuItem
                      key={m.id}
                      onSelect={() => onSwitchMode(m.id)}
                      className="flex-col items-start gap-0.5"
                    >
                      <span className={cn("text-[13px]", m.id === currentMode?.id && "font-medium")}>
                        {m.label}
                        {m.id === currentMode?.id && " ✓"}
                      </span>
                      {m.description && (
                        <span className="text-muted-foreground text-[11px] leading-snug whitespace-normal">
                          {m.description}
                        </span>
                      )}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>

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
          {onSchedule && <Button variant="ghost" size="icon" className="ml-1 size-11 shrink-0 md:size-8" aria-label="Schedule send" title="Schedule send" disabled={disabled || sendDisabled || uploading || (!draft.trim() && sendableImages === 0 && blobs.length === 0)} onClick={onSchedule}><ClockIcon className="size-4" /></Button>}
          {/* Sending while a turn runs hands the message to the harness,
              which reads it at its next step. The button only appears once
              there is something to send, so an idle-looking stop button is
              not crowded by a dead send. */}
          {/* How the message reaches a turn that is already running. Only
              while one is: on an idle session there is nothing to interrupt
              and nothing to wait for, and a dead control beside send is worse
              than no control. The same menu opens on a long press of send, so
              a thumb never has to find the 11px chevron. */}
          {busy && (
            <DropdownMenu open={deliveryMenuOpen} onOpenChange={setDeliveryMenuOpen}>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={disabled}
                  aria-label={`Delivery: ${deliveryLabel(delivery)}`}
                  title={`Delivery: ${deliveryLabel(delivery)}`}
                  className="text-muted-foreground hover:text-foreground ml-1 size-11 shrink-0 rounded-full md:size-8"
                >
                  <ChevronUpIcon className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-w-[min(22rem,calc(100vw-2rem))]">
                <DropdownMenuLabel>Send this message</DropdownMenuLabel>
                {DELIVERY_OPTIONS.map((o) => (
                  <DropdownMenuItem
                    key={o.id}
                    onSelect={() => chooseDelivery(o.id)}
                    className="flex-col items-start gap-0.5"
                  >
                    <span className={cn("text-[13px]", o.id === delivery && "font-medium")}>
                      {o.label}
                      {o.id === delivery && " ✓"}
                    </span>
                    <span className="text-muted-foreground text-[11px] leading-snug whitespace-normal">
                      {o.description}
                    </span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {(!busy || draft.trim() || sendableImages > 0 || blobs.length > 0) && (
            <Button
              size="icon"
              disabled={disabled || sendDisabled || uploading || (!draft.trim() && sendableImages === 0 && blobs.length === 0)}
              onClick={() => {
                // A long press has already opened the menu; the click that
                // ends it must not also send.
                if (longPressed.current) {
                  longPressed.current = false;
                  return;
                }
                void send();
              }}
              onPointerDown={() => {
                if (!busy) return;
                longPressed.current = false;
                longPress.current = window.setTimeout(() => {
                  longPressed.current = true;
                  setDeliveryMenuOpen(true);
                }, 450);
              }}
              onPointerUp={cancelLongPress}
              onPointerLeave={cancelLongPress}
              onContextMenu={(e) => {
                // iOS raises the callout on a long press; the menu is the
                // answer we want instead.
                if (busy) e.preventDefault();
              }}
              aria-label={busy ? `Send to the running turn — ${deliveryLabel(delivery)}` : "Send"}
              title={busy ? DELIVERY_OPTIONS.find((o) => o.id === delivery)?.description : undefined}
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
