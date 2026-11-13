import { ChevronDownIcon, SearchIcon, ZapIcon } from "lucide-react";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
} from "react";

import { ChunkFallback } from "~/components/ChunkFallback";
import type { ModelListProps } from "~/components/ModelList";
import { ProviderLogo } from "~/components/ProviderLogo";
import { Button } from "~/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "~/components/ui/sheet";
import { Switch } from "~/components/ui/switch";
import { chunk } from "~/lib/chunk";
import { pickerInstances, resolveInstance, resolveModel, type PickerInstance } from "~/lib/models";
import { formatEffort } from "~/lib/efforts";
import { cn } from "~/lib/utils";
import { useChunk, useIdlePrefetch } from "~/useChunk";
import { useIsDesktop } from "~/useMediaQuery";
import type { HarnessMeta, ModelMeta } from "~/protocol";

/** What the picker returns: choosing a model chooses its account too. */
export interface ModelSelection {
  /** The driver, which is what a thread records as its harness. */
  harness: string;
  /** The provider instance the thread runs under. */
  instance: string;
  model: string;
}

/** Where focus lands when the picker is opened for a purpose. */
export type PickerFocus = "model" | "effort";

const NO_EFFORTS: string[] = [];

// The search and the model list are built on cmdk, which stays out of the
// first load. The picker fetches them once the page is idle, so opening it
// rarely waits; when it does, a stand-in search takes the typing.
export const modelList = chunk(() => import("./ModelList").then((m) => m.ModelList));

interface ModelPickerProps {
  harnesses: HarnessMeta[];
  value: ModelSelection;
  onChange: (next: ModelSelection) => void;
  /**
   * Mid-thread the harness is fixed — another one is a different agent — but
   * the account is not: the rail offers only this harness's other enabled
   * accounts, and disappears when there are none. Picking a model under
   * another account is a request to switch the thread to it.
   */
  lockDriver?: boolean;
  /** New-thread preferences can select an account immediately on a rail click. */
  onInstanceChange?: (instance: PickerInstance) => void;
  disabled?: boolean;
  /**
   * The running model's reasoning levels, most modest first. Empty — a legacy
   * model that accepts none, or a caller with no business offering them — and
   * the effort row simply does not appear.
   */
  efforts?: string[];
  /** The active effort, or "" for no level named. */
  effort?: string;
  /** The running model's context window, already formatted ("1M"), or "". */
  contextLabel?: string;
  onEffortChange?: (effort: string) => void;
  /**
   * The service tier that runs the selected model faster, or "" when it has
   * none — and then, as with efforts, the Fast row does not appear, unless a
   * tier is still set and the row is needed to turn it off.
   */
  fastTier?: string;
  /** The active service tier: "" for standard speed. */
  serviceTier?: string;
  onServiceTierChange?: (serviceTier: string) => void;
  id?: string;
  className?: string;
  /**
   * The trigger names only the model: the logo, the effort, the chevron and
   * the word "Fast" go, so the name keeps what width a narrow composer has.
   * All of them are still one tap away in the picker.
   */
  compact?: boolean;
  /** Optional controlled state, used by composer actions such as /model. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /**
   * Where focus should be: the search, or the effort level in effect. Each
   * request is a new object, so asking again while open moves focus again.
   * Null leaves focus where opening puts it, on the search.
   */
  focusRequest?: { target: PickerFocus } | null;
  /**
   * Opens the picker in place of the built-in button, for a caller adding a
   * model rather than showing one. A single element that takes a ref and
   * props, since the popover or sheet hangs its trigger wiring on it.
   */
  trigger?: ReactNode;
}

/**
 * The one control for choosing a harness account and a model.
 *
 * They were two controls, and choosing a model already implies its harness, so
 * they are one: a rail of provider instances beside that instance's models.
 * Every row says what the model is and what it is for, because the old
 * single-line list could not distinguish two Opuses — or say what "Default"
 * resolved to.
 *
 * Reasoning effort joins it on the same terms. It is a second axis, not a
 * second control: passing the effort props adds a row of levels at the foot of
 * this menu, and the trigger then names the model and the level together. A
 * composer that had two dropdowns elbowing the send button has one.
 *
 * Picking a model leaves the menu open when there is an effort still to
 * choose, so changing both is one visit; picking the effort, the last choice,
 * closes it.
 *
 * Fast is the third axis, and the smallest: an on/off switch beside effort for
 * a model whose harness can run it on a faster service tier. The trigger
 * carries a bolt while it is on, since it costs more of the plan per turn.
 */
export function ModelPicker({
  harnesses,
  value,
  onChange,
  onInstanceChange,
  lockDriver = false,
  disabled = false,
  efforts = NO_EFFORTS,
  effort = "",
  contextLabel = "",
  onEffortChange,
  fastTier = "",
  serviceTier = "",
  onServiceTierChange,
  id,
  className,
  compact = false,
  open: controlledOpen,
  onOpenChange,
  focusRequest = null,
  trigger: customTrigger,
}: ModelPickerProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setInternalOpen(next);
    onOpenChange?.(next);
  };
  const isDesktop = useIsDesktop();

  const instances = useMemo(() => {
    const all = pickerInstances(harnesses);
    if (!lockDriver) return all;
    const own = all.filter((i) => i.driver === value.harness);
    const current = resolveInstance(own, value.instance, value.harness)?.id;
    return own.filter((i) => i.enabled || i.id === current);
  }, [harnesses, lockDriver, value.harness, value.instance]);
  const selectedInstance = resolveInstance(instances, value.instance, value.harness);
  const selectedModel = resolveModel(selectedInstance, value.model);

  // Which instance's models the right pane shows. It follows the selection
  // until the user browses another account, which is a look rather than a
  // choice: nothing changes until a model is picked.
  const selectedId = selectedInstance?.id ?? "";
  const [browsing, setBrowsing] = useState(selectedId);
  const [search, setSearch] = useState("");
  // Whether the stand-in's search has focus, so the real one can take it over.
  const [searchHeld, setSearchHeld] = useState(false);
  // Opening the picker, or the selection moving while it is open, starts it
  // over at the selection with no query. Adjusted during render so the menu
  // never paints a stale pane first.
  const [synced, setSynced] = useState({ open, selectedId });
  if (synced.open !== open || synced.selectedId !== selectedId) {
    setSynced({ open, selectedId });
    if (open) {
      setBrowsing(selectedId);
      setSearch("");
      setSearchHeld(false);
    }
  }
  useIdlePrefetch(modelList);
  const { loaded: LoadedList, failed } = useChunk(modelList, open);

  const shown = instances.find((i) => i.id === browsing) ?? selectedInstance;

  const showEffort = efforts.length > 0 && !!onEffortChange;
  // A model that offers no levels still ran at one, and that is still half of
  // what this control is naming — so the trigger says it, and only the row
  // that switches it goes away.
  const namesEffort = showEffort || !!effort;
  // Named on the trigger whenever the thread runs on a tier, for the same
  // reason as effort: a model that cannot switch it still ran on it.
  const fast = !!serviceTier;
  // A thread can be left on a tier its model does not offer (an account
  // switch moves the model without asking), so the row stays while one is
  // set: it is the only way back off.
  const showFast = (!!fastTier || fast) && !!onServiceTierChange;

  const choose = (instance: PickerInstance, model: ModelMeta) => {
    // A model with levels to pick leaves the effort row still to choose. One
    // without, or a caller offering none, makes this pick the last choice.
    if (!onEffortChange || !model.efforts?.length) setOpen(false);
    onChange({ harness: instance.driver, instance: instance.id, model: model.id });
  };

  const searchRef = useRef<HTMLInputElement>(null);
  const effortRef = useRef<HTMLDivElement>(null);
  /** Focuses the target if it is on screen; says whether it was. */
  const focusOn = (target: PickerFocus) => {
    const el =
      target === "effort"
        ? effortRef.current?.querySelector<HTMLElement>('[role="radio"][tabindex="0"]')
        : searchRef.current;
    el?.focus();
    return !!el;
  };
  // Opening focuses the search by itself, so a request for effort has to
  // overrule that as the content mounts. A model with no levels has no effort
  // row, and the search keeps focus.
  const openAutoFocus = (event: Event) => {
    if (focusRequest?.target === "effort" && focusOn("effort")) event.preventDefault();
  };
  // Already open, nothing mounts and nothing autofocuses: the request moves
  // focus itself. On the render that opens the picker the content is not
  // mounted yet, so this finds nothing and the handler above does the work.
  useEffect(() => {
    if (open && focusRequest) focusOn(focusRequest.target);
    // focusOn reads refs only; the request object is the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, focusRequest]);

  const listProps: ModelListProps = {
    instances,
    shown,
    selectedInstance,
    selectedModel,
    search,
    placeholder: instances.length > 1 ? "Search models and accounts…" : "Search models…",
    onSearchChange: setSearch,
    searchRef,
    takeFocus: searchHeld,
    onBrowse: (id) => {
      setBrowsing(id);
      const target = instances.find((i) => i.id === id);
      if (target) onInstanceChange?.(target);
    },
    onChoose: choose,
  };
  const body = LoadedList ? (
    <LoadedList {...listProps} />
  ) : (
    <ListStandIn {...listProps} failed={failed} onHeldChange={setSearchHeld} />
  );

  const footer = (showEffort || showFast || contextLabel) && (
    <PickerFooter
      effortRef={effortRef}
      efforts={showEffort ? efforts : NO_EFFORTS}
      effort={effort}
      onChooseEffort={(next) => {
        setOpen(false);
        if (next !== effort) onEffortChange?.(next);
      }}
      showFast={showFast}
      fast={fast}
      onFastChange={(on) => onServiceTierChange?.(on ? fastTier : "")}
      contextLabel={contextLabel}
    />
  );

  const trigger = customTrigger ?? (
    <Button
      id={id}
      variant="outline"
      role="combobox"
      aria-expanded={open}
      aria-label="Harness and model"
      disabled={disabled}
      // Same field height as every other control in the form it sits in: 44px
      // on a phone, 36px from md up. It was a 40px control in a column of
      // 36px ones.
      className={cn("h-auto min-h-11 w-full justify-start gap-2 px-3 py-2 md:min-h-9", className)}
    >
      {selectedInstance && (
        <ProviderLogo provider={selectedInstance.driver} className={cn(compact && "hidden")} />
      )}
      <TriggerLabel
        model={selectedModel}
        effort={namesEffort ? effort : undefined}
        fast={fast}
        compact={compact}
      />
      <ChevronDownIcon className={cn("text-muted-foreground size-4 shrink-0", compact && "hidden")} />
    </Button>
  );

  // A two-pane popover has nowhere to go on a phone, so the same content
  // arrives as a bottom sheet with room for touch targets.
  if (!isDesktop) {
    return (
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent
          side="bottom"
          onOpenAutoFocus={openAutoFocus}
          className="flex h-[85dvh] flex-col p-0 pb-[env(safe-area-inset-bottom)]"
        >
          <SheetHeader className="pb-0">
            <SheetTitle>Model</SheetTitle>
          </SheetHeader>
          {body}
          {footer}
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent
        align="start"
        onOpenAutoFocus={openAutoFocus}
        className="w-[min(34rem,calc(100vw-2rem))] p-0"
      >
        {body}
        {footer}
      </PopoverContent>
    </Popover>
  );
}

/**
 * The foot of the menu: effort, Fast, and the context window, each only when
 * there is one to show.
 *
 * Deliberately outside the model list's Command. cmdk claims arrows and Enter
 * for the whole of its root, so a button nested in it cannot be operated from
 * the keyboard: Enter would select the highlighted model instead.
 */
function PickerFooter({
  effortRef,
  efforts,
  effort,
  onChooseEffort,
  showFast,
  fast,
  onFastChange,
  contextLabel,
}: {
  effortRef: RefObject<HTMLDivElement | null>;
  /** The levels to offer; none leaves the effort row out. */
  efforts: string[];
  effort: string;
  onChooseEffort: (effort: string) => void;
  showFast: boolean;
  fast: boolean;
  onFastChange: (on: boolean) => void;
  contextLabel: string | undefined;
}) {
  return (
    <div className="border-t">
      {efforts.length > 0 && (
        <EffortLevels groupRef={effortRef} efforts={efforts} value={effort} onChoose={onChooseEffort} />
      )}
      {/* Unlike effort, this leaves the menu open: a switch reads as settled
          once it has moved, and closing would hide that it did. */}
      {showFast && (
        <div className="p-1">
          <FastRow on={fast} onChange={onFastChange} />
        </div>
      )}
      {contextLabel && <ContextNote label={contextLabel} />}
    </div>
  );
}

/**
 * The reasoning levels, as a row of choices at the foot of the menu.
 *
 * It was a submenu: a popover beside the model menu on desktop and a second
 * sheet stacked on the first on a phone, which was a lot of ceremony for
 * five words. A row puts every level in view at once and costs one tap.
 *
 * A radio group, but arrows only move focus: picking closes the menu, so a
 * group that picked as you arrowed through it would close on the first press.
 * Enter or Space picks the focused level.
 */
function EffortLevels({
  groupRef,
  efforts,
  value,
  onChoose,
}: {
  groupRef: RefObject<HTMLDivElement | null>;
  /** Levels the running model accepts, most modest first. */
  efforts: string[];
  /** The active effort, or "" for no level named. */
  value: string;
  onChoose: (effort: string) => void;
}) {
  // "" first: the way back to the harness default, and the level you get by
  // not choosing.
  const levels = ["", ...efforts];
  // A level this model does not offer checks nothing, and the group is still
  // reachable by Tab, from its first level.
  const tabStop = Math.max(0, levels.indexOf(value));

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const radios = [...event.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]')];
    const at = radios.indexOf(document.activeElement as HTMLElement);
    const last = radios.length - 1;
    const moves: Record<string, number> = {
      ArrowRight: at >= last ? 0 : at + 1,
      ArrowDown: at >= last ? 0 : at + 1,
      ArrowLeft: at <= 0 ? last : at - 1,
      ArrowUp: at <= 0 ? last : at - 1,
      Home: 0,
      End: last,
    };
    const next = moves[event.key];
    if (next === undefined) return;
    event.preventDefault();
    radios[next]?.focus();
  };

  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-2 py-1.5">
      <span aria-hidden className="text-muted-foreground px-1 text-[12px]">
        Effort
      </span>
      <div
        ref={groupRef}
        role="radiogroup"
        aria-label="Reasoning effort"
        onKeyDown={onKeyDown}
        className="flex flex-wrap gap-1"
      >
        {levels.map((level, i) => {
          const checked = level === value;
          return (
            <button
              key={level || "auto"}
              type="button"
              role="radio"
              aria-checked={checked}
              tabIndex={i === tabStop ? 0 : -1}
              title={level ? undefined : "Whatever the harness picks"}
              onClick={() => onChoose(level)}
              className={cn(
                "focus-visible:ring-ring min-h-11 rounded-md px-3 text-[13px] transition-colors outline-none focus-visible:ring-2 md:min-h-7 md:px-2 md:text-[12px]",
                checked
                  ? "bg-accent text-accent-foreground font-medium"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
            >
              {formatEffort(level)}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/**
 * What the trigger says: the model, then the effort it runs at when there is
 * one to name, then a bolt while it runs Fast.
 */
function TriggerLabel({
  model,
  effort,
  fast,
  compact,
}: {
  model: ModelMeta | undefined;
  /** The effort to name, or undefined when there is none to name. */
  effort: string | undefined;
  fast: boolean;
  compact: boolean;
}) {
  const named = effort !== undefined;
  return (
    // Baseline-aligned: the effort is set smaller, and centring the two boxes
    // left its text sitting higher than the model's.
    <span className="flex min-w-0 flex-1 items-baseline gap-2">
      <span className="min-w-0 flex-1 truncate text-left text-[13px]">
        {model?.label ?? "No model"}
        {/* The generation competes for one line with the effort and the bolt,
            and is the only one of them that is not a knob: when either is on
            show, the generation stays in the list, where the row that names
            it also explains it. */}
        {!named && !fast && model?.version && model.version !== model.label && (
          <span className="text-muted-foreground ml-1.5 text-[11px]">{model.version}</span>
        )}
      </span>
      {named && !compact && (
        <span className="text-muted-foreground shrink-0 text-[12px]">
          <span aria-hidden className="mr-1.5 opacity-60">
            ·
          </span>
          {formatEffort(effort)}
        </span>
      )}
      {/* Compact, the bolt says it alone, so the model's name keeps the
          width; the word is still there for a screen reader. */}
      {fast && (
        <span className="flex shrink-0 items-center gap-1 self-center text-[12px] text-amber-600 dark:text-amber-400">
          <ZapIcon aria-hidden className="size-3.5 fill-current" />
          <span className={cn(compact && "sr-only")}>Fast</span>
        </span>
      )}
    </span>
  );
}

/**
 * The running model's context window, stated where the model is chosen. It is
 * a fact of the model rather than a knob — the harness fixes the window per
 * model — so it is said, never offered.
 */
function ContextNote({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground border-t px-3 py-2 text-[11px] first:border-t-0">
      Context window: <span className="text-foreground">{label}</span>
      <span className="block text-[10px]">Set by the model — not adjustable.</span>
    </div>
  );
}

/**
 * The Fast switch. The whole row is its label, so a phone gets a full-width
 * target rather than a 32px switch.
 */
function FastRow({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  const id = useId();
  return (
    <label
      htmlFor={id}
      className="hover:bg-accent hover:text-accent-foreground flex min-h-11 w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-[13px] transition-colors md:min-h-9"
    >
      <span className="min-w-0 flex-1">
        <span className="block">Fast</span>
        <span className="text-muted-foreground block text-[11px]">
          Faster responses, uses more of your plan
        </span>
      </span>
      <Switch id={id} aria-label="Fast mode" checked={on} onCheckedChange={onChange} />
    </label>
  );
}

/**
 * What the picker shows while its list is on the way: a search that looks
 * like the real one and keeps what is typed into it, so a first open on a
 * slow link loses no keys, and a spinner where the models will be.
 */
function ListStandIn({
  search,
  placeholder,
  onSearchChange,
  searchRef,
  failed,
  onHeldChange,
}: ModelListProps & { failed: boolean; onHeldChange: (held: boolean) => void }) {
  return (
    // Fills the sheet as the real list does, so the footer does not jump.
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-3">
        <SearchIcon className="size-4 shrink-0 opacity-50" />
        <input
          ref={searchRef}
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          onFocus={() => onHeldChange(true)}
          onBlur={() => onHeldChange(false)}
          // Nothing to pick yet; the real list takes Enter, so this one must
          // not submit a form it sits in.
          onKeyDown={(e) => {
            if (e.key === "Enter") e.preventDefault();
          }}
          placeholder={placeholder}
          aria-label={placeholder}
          autoComplete="off"
          spellCheck={false}
          className="placeholder:text-muted-foreground flex h-full w-full rounded-md bg-transparent text-sm outline-hidden"
        />
      </div>
      <ChunkFallback failed={failed} className="flex-1 p-3 text-[12px]" />
    </div>
  );
}
