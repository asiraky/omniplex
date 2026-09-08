import { CheckIcon, ChevronDownIcon, ChevronRightIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import { ContextNote, EffortMenu } from "~/components/EffortMenu";
import { PROVIDER_LOGOS, ProviderLogo } from "~/components/ProviderLogo";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent } from "~/components/ui/collapsible";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "~/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "~/components/ui/sheet";
import {
  defaultModel,
  isLegacy,
  pickerInstances,
  resolveInstance,
  resolveModel,
  type PickerInstance,
} from "~/lib/models";
import { formatEffort } from "~/lib/efforts";
import { rankModels, type ModelRow } from "~/lib/modelSearch";
import { cn } from "~/lib/utils";
import { useIsDesktop } from "~/useMediaQuery";
import type { HarnessMeta, ModelMeta } from "~/protocol";

/** What the picker returns: choosing a model chooses its account too. */
export interface ModelSelection {
  /** The driver, which is what a session records as its harness. */
  harness: string;
  /** The provider instance the session runs under. */
  instance: string;
  model: string;
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
 * second control: passing the effort props adds a row at the foot of this menu
 * that opens the levels as a submenu, and the trigger then names the model and
 * the level together. A composer that had two dropdowns elbowing the send
 * button has one.
 */
export function ModelPicker({
  harnesses,
  value,
  onChange,
  onInstanceChange,
  lockInstance = false,
  disabled = false,
  efforts = [],
  effort = "",
  contextLabel = "",
  onEffortChange,
  id,
  className,
  open: controlledOpen,
  onOpenChange,
}: {
  harnesses: HarnessMeta[];
  value: ModelSelection;
  onChange: (next: ModelSelection) => void;
  /**
   * Mid-session the account is fixed — the harness is already running under
   * it — so only the model can change and the rail becomes a label.
   */
  lockInstance?: boolean;
  /** New-session preferences can select an account immediately on a rail click. */
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
  id?: string;
  className?: string;
  /** Optional controlled state, used by composer actions such as /model. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setInternalOpen(next);
    onOpenChange?.(next);
  };
  const isDesktop = useIsDesktop();

  const instances = useMemo(() => pickerInstances(harnesses), [harnesses]);
  const selectedInstance = resolveInstance(instances, value.instance, value.harness);
  const selectedModel = resolveModel(selectedInstance, value.model);

  // Which instance's models the right pane shows. It follows the selection
  // until the user browses another account, which is a look rather than a
  // choice: nothing changes until a model is picked.
  const [browsing, setBrowsing] = useState(selectedInstance?.id ?? "");
  const [search, setSearch] = useState("");
  useEffect(() => {
    if (!open) return;
    setBrowsing(selectedInstance?.id ?? "");
    setSearch("");
  }, [open, selectedInstance?.id]);

  const shown = lockInstance
    ? selectedInstance
    : (instances.find((i) => i.id === browsing) ?? selectedInstance);

  const searching = search.trim().length > 0;
  // Search deliberately ignores the rail: a query spans every account, so
  // typing a model name finds it without knowing which account it is under.
  const matches = useMemo(() => {
    if (!searching) return [];
    const pool = lockInstance && shown ? [shown] : instances;
    return rankModels(rowsOf(pool), search);
  }, [searching, search, instances, lockInstance, shown]);

  const showEffort = efforts.length > 0 && !!onEffortChange;
  // A model that offers no levels still ran at one, and that is still half of
  // what this control is naming — so the trigger says it, and only the row
  // that switches it goes away.
  const namesEffort = showEffort || !!effort;

  const choose = (instance: PickerInstance, model: ModelMeta) => {
    setOpen(false);
    onChange({ harness: instance.driver, instance: instance.id, model: model.id });
  };

  const body = (
    <Command
      // Ranking is ours: cmdk's own filter cannot see the instance a row
      // belongs to, and the legacy split has to collapse while searching.
      shouldFilter={false}
      className="bg-transparent"
    >
      <CommandInput
        value={search}
        onValueChange={setSearch}
        placeholder={lockInstance ? "Search models…" : "Search models and accounts…"}
      />
      <div className="flex min-h-0 flex-1 flex-col sm:flex-row">
        {!lockInstance && instances.length > 1 && (
          <InstanceRail
            instances={instances}
            browsing={shown?.id ?? ""}
            selected={selectedInstance?.id ?? ""}
            onBrowse={(id) => {
              setBrowsing(id);
              const target = instances.find((i) => i.id === id);
              if (target) onInstanceChange?.(target);
            }}
          />
        )}
        <CommandList className="max-h-[min(60dvh,22rem)] flex-1">
          <CommandEmpty>No model matches that.</CommandEmpty>
          {searching ? (
            <CommandGroup>
              {matches.map((row) => (
                <ModelRowItem
                  key={`${row.instance}:${row.model.id}`}
                  model={row.model}
                  instance={row.ref}
                  // While searching, rows come from every account, so each one
                  // says which — the rail is not telling you any more.
                  showInstance={!lockInstance && instances.length > 1}
                  selected={
                    row.instance === selectedInstance?.id && row.model.id === selectedModel?.id
                  }
                  onChoose={choose}
                />
              ))}
            </CommandGroup>
          ) : (
            shown && (
              <InstanceModels
                instance={shown}
                selectedModelId={shown.id === selectedInstance?.id ? (selectedModel?.id ?? "") : ""}
                onChoose={choose}
              />
            )
          )}
        </CommandList>
      </div>
    </Command>
  );

  // Deliberately outside the Command above. cmdk claims arrows and Enter for
  // the whole of its root, so a button nested in it cannot be operated from
  // the keyboard: Enter would select the highlighted model instead.
  const footer = (showEffort || contextLabel) && (
    <div className="border-t">
      {showEffort && (
        <div className="p-1">
          <EffortMenu
            efforts={efforts}
            value={effort}
            onChange={(next) => {
              setOpen(false);
              onEffortChange?.(next);
            }}
          />
        </div>
      )}
      {contextLabel && <ContextNote label={contextLabel} />}
    </div>
  );

  const trigger = (
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
      {selectedInstance && <ProviderLogo provider={selectedInstance.driver} />}
      <span className="min-w-0 flex-1 truncate text-left text-[13px]">
        {selectedModel?.label ?? "No model"}
        {/* The generation and the effort compete for one line, and only one of
            them is a knob: when effort is on show, the generation stays in the
            list, where the row that names it also explains it. */}
        {!namesEffort && selectedModel?.version && selectedModel.version !== selectedModel.label && (
          <span className="text-muted-foreground ml-1.5 text-[11px]">{selectedModel.version}</span>
        )}
      </span>
      {namesEffort && (
        <span className="text-muted-foreground shrink-0 text-[12px]">
          <span aria-hidden className="mr-1.5 opacity-60">
            ·
          </span>
          {formatEffort(effort)}
        </span>
      )}
      <ChevronDownIcon className="text-muted-foreground size-4 shrink-0" />
    </Button>
  );

  // A two-pane popover has nowhere to go on a phone, so the same content
  // arrives as a bottom sheet with room for touch targets.
  if (!isDesktop) {
    return (
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent side="bottom" className="flex h-[85dvh] flex-col p-0 pb-[env(safe-area-inset-bottom)]">
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
      <PopoverContent align="start" className="w-[min(34rem,calc(100vw-2rem))] p-0">
        {body}
        {footer}
      </PopoverContent>
    </Popover>
  );
}

/**
 * The rail of accounts. Instances, not products: a built-in Codex and a second
 * Codex account are two entries routing to their own model lists, and an
 * unavailable one still shows — dimmed, with the harness's own reason — since
 * a silently missing account reads as a bug.
 */
function InstanceRail({
  instances,
  browsing,
  selected,
  onBrowse,
}: {
  instances: PickerInstance[];
  browsing: string;
  selected: string;
  onBrowse: (id: string) => void;
}) {
  const duplicated = new Set(
    instances.map((i) => i.driver).filter((driver, index, all) => all.indexOf(driver) !== index),
  );

  return (
    <div className="flex shrink-0 gap-1 overflow-x-auto border-b p-2 sm:w-44 sm:flex-col sm:overflow-x-visible sm:overflow-y-auto sm:border-r sm:border-b-0">
      {instances.map((instance) => {
        const ready = instance.availability?.state === "ready";
        return (
          <button
            key={instance.id}
            type="button"
            onClick={() => onBrowse(instance.id)}
            aria-pressed={browsing === instance.id}
            title={
              ready
                ? instance.name
                : `${instance.name} — ${instance.availability?.reason ?? "Unavailable"}`
            }
            style={
              instance.accent
                ? ({ "--provider-accent": instance.accent } as CSSProperties)
                : undefined
            }
            className={cn(
              "focus-visible:ring-ring flex min-h-11 shrink-0 items-center gap-2 rounded-lg px-2.5 text-left text-[13px] transition-colors outline-none focus-visible:ring-2 sm:min-h-9 sm:w-full",
              browsing === instance.id ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
              !ready && "opacity-50",
            )}
          >
            <InstanceMark instance={instance} initials={duplicated.has(instance.driver)} />
            <span className="truncate">{instance.name}</span>
            {selected === instance.id && (
              <CheckIcon className="text-muted-foreground ml-auto size-3.5 shrink-0" />
            )}
          </button>
        );
      })}
    </div>
  );
}

/**
 * A provider's mark, or its initials when the logo map has no entry — which is
 * what makes a brand-new harness render sensibly before anyone has drawn it
 * one. When a driver has more than one account, the mark is followed by
 * initials, so two Codex rows differ by more than the words next to them.
 */
function InstanceMark({ instance, initials }: { instance: PickerInstance; initials: boolean }) {
  const mark = PROVIDER_LOGOS[instance.driver] ? (
    <ProviderLogo provider={instance.driver} />
  ) : (
    <Initials name={instance.name} />
  );
  if (!initials || !PROVIDER_LOGOS[instance.driver]) return mark;
  return (
    <>
      {mark}
      <Initials name={instance.name} />
    </>
  );
}

function Initials({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="bg-muted text-muted-foreground grid size-4 shrink-0 place-items-center rounded text-[9px] font-medium"
    >
      {initialsOf(name)}
    </span>
  );
}

function initialsOf(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}

/**
 * One account's models: current first, then a Legacy group that stays folded
 * until asked for — unless the current selection is itself legacy, in which
 * case hiding it would hide where you are.
 */
function InstanceModels({
  instance,
  selectedModelId,
  onChoose,
}: {
  instance: PickerInstance;
  selectedModelId: string;
  onChoose: (instance: PickerInstance, model: ModelMeta) => void;
}) {
  const current = instance.models.filter((m) => !isLegacy(m));
  const legacy = instance.models.filter(isLegacy);
  const selectedIsLegacy = legacy.some((m) => m.id === selectedModelId);
  const [expanded, setExpanded] = useState(selectedIsLegacy);
  useEffect(() => setExpanded(selectedIsLegacy), [instance.id, selectedIsLegacy]);

  // Expanding below the fold would look like nothing happened, so the group
  // scrolls itself into view.
  const legacyRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (expanded) legacyRef.current?.scrollIntoView({ block: "nearest" });
  }, [expanded]);

  return (
    <>
      <CommandGroup>
        {current.map((model) => (
          <ModelRowItem
            key={model.id}
            model={model}
            instance={instance}
            selected={model.id === selectedModelId}
            onChoose={onChoose}
          />
        ))}
      </CommandGroup>
      {legacy.length > 0 && (
        <Collapsible open={expanded}>
          <CommandGroup>
            <CommandItem
              value="__legacy__"
              onSelect={() => setExpanded((e) => !e)}
              className="text-muted-foreground gap-1.5 text-[12px]"
            >
              {expanded ? (
                <ChevronDownIcon className="size-3.5" />
              ) : (
                <ChevronRightIcon className="size-3.5" />
              )}
              Legacy ({legacy.length})
            </CommandItem>
          </CommandGroup>
          <CollapsibleContent ref={legacyRef}>
            <CommandGroup>
              {legacy.map((model) => (
                <ModelRowItem
                  key={model.id}
                  model={model}
                  instance={instance}
                  selected={model.id === selectedModelId}
                  onChoose={onChoose}
                />
              ))}
            </CommandGroup>
          </CollapsibleContent>
        </Collapsible>
      )}
    </>
  );
}

/** Name and generation on one line, the harness's own description beneath. */
function ModelRowItem({
  model,
  instance,
  selected,
  showInstance = false,
  onChoose,
}: {
  model: ModelMeta;
  instance: PickerInstance;
  selected: boolean;
  showInstance?: boolean;
  onChoose: (instance: PickerInstance, model: ModelMeta) => void;
}) {
  return (
    <CommandItem
      value={`${instance.id}:${model.id}`}
      onSelect={() => onChoose(instance, model)}
      className="min-h-11 items-start gap-2 py-2 md:min-h-0"
    >
      <CheckIcon className={cn("mt-0.5 size-4 shrink-0", !selected && "opacity-0")} />
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="truncate text-[13px]">{model.label}</span>
          {model.version && model.version !== model.label && (
            <span className="text-muted-foreground ml-auto shrink-0 text-[11px]">
              {model.version}
            </span>
          )}
        </span>
        {model.description && (
          <span className="text-muted-foreground block truncate text-[11px]">
            {model.description}
          </span>
        )}
        {showInstance && (
          <span className="text-muted-foreground/80 mt-0.5 flex items-center gap-1 text-[11px]">
            <ProviderLogo provider={instance.driver} className="size-3" />
            {instance.name}
          </span>
        )}
      </span>
    </CommandItem>
  );
}

/** Every model of every account in the pool, tagged with where it came from. */
function rowsOf(instances: PickerInstance[]): (ModelRow & { ref: PickerInstance })[] {
  return instances.flatMap((instance) =>
    instance.models.map((model) => ({
      instance: instance.id,
      instanceName: instance.name,
      driver: instance.driver,
      model,
      ref: instance,
    })),
  );
}

export { defaultModel };
