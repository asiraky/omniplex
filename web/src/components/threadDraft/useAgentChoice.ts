import type { ThreadAgent } from "~/app/useThreadSelection";
import type { ModelSelection } from "~/components/ModelPicker";
import {
  defaultModel,
  pickerInstances,
  resolveInstance,
  resolveModel as matchModel,
  type PickerInstance,
} from "~/lib/models";
import { modeForLevel } from "~/lib/permissions";
import type { HarnessPrefs, ProjectPrefs } from "~/lib/threadPrefs";
import type { HarnessMeta, Project, ProjectDefaults, UserConfig } from "~/protocol";
import type { RememberedChoices } from "./useRememberedChoices";

/** A project's starting choices for one harness, from its settings. */
type Seed = NonNullable<ProjectDefaults["harnesses"]>[string];

export interface AgentSources {
  project: Project | undefined;
  remembered: ProjectPrefs | undefined;
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
}

/** The harness and account the thread runs under. */
function resolveHarness({ project, remembered, harnesses, userConfig }: AgentSources) {
  const instances = pickerInstances(harnesses);
  const fallbackHarness =
    (harnesses.some((h) => h.id === remembered?.harness) ? remembered?.harness : "") ||
    project?.defaults.harness ||
    instances.find((i) => i.id === userConfig?.defaultInstance)?.driver ||
    harnesses.find((h) => h.availability.state === "ready")?.id ||
    harnesses[0]?.id ||
    "";
  const instance = resolveInstance(
    instances,
    remembered?.byHarness[fallbackHarness]?.instance ?? userConfig?.defaultInstance ?? "",
    fallbackHarness,
  );
  const harnessId = instance?.driver ?? fallbackHarness;
  return { instances, instance, harnessId };
}

/** The model, its context window, its effort and whether it runs Fast. */
function resolveModel(
  instance: PickerInstance | undefined,
  chosen: Partial<HarnessPrefs> | undefined,
  harnessDefaults: Seed | undefined,
  userConfig: UserConfig | null,
) {
  const want1m = chosen?.want1m ?? false;
  // A model the account no longer offers is not sent: the harness's own
  // default is a better answer than a name it has stopped serving.
  const preferred =
    chosen?.model ??
    harnessDefaults?.model ??
    (instance && instance.id === userConfig?.defaultInstance ? userConfig.defaultModel : "") ??
    "";
  const model = instance?.models.some((m) => m.id === preferred)
    ? preferred
    : (defaultModel(instance)?.id ?? "");
  const modelMeta = instance?.models.find((m) => m.id === model);
  // The adapter marks the models it saw a "[1m]" alias for; that tag on the
  // model id is what it turns into the context-window setting at start.
  const supports1m = modelMeta?.supports1m ?? false;
  const effectiveModel = supports1m && want1m ? `${model}[1m]` : model;
  const efforts = modelMeta?.efforts ?? [];
  const preferredEffort = chosen?.effort ?? harnessDefaults?.effort ?? "";
  const effort = efforts.includes(preferredEffort) ? preferredEffort : "";
  // Fast is kept as a wish and turned into this model's own tier id at send,
  // so it survives a pick that has no fast tier.
  const wantFast = chosen?.fast ?? false;
  const fastTier = modelMeta?.fastTier ?? "";
  const serviceTier = wantFast && fastTier ? fastTier : "";
  return {
    model,
    want1m,
    supports1m,
    effectiveModel,
    effort,
    preferredEffort,
    wantFast,
    serviceTier,
  };
}

/** The mode for a harness with none saved: the project's seed, else the level from settings. */
function unsavedMode(
  selected: HarnessMeta | undefined,
  harnessDefaults: Seed | undefined,
  userConfig: UserConfig | null,
) {
  return (
    harnessDefaults?.mode ?? modeForLevel(selected?.permissionModes ?? [], userConfig?.defaultLevel)
  );
}

/** The permission mode, as sent and as shown. */
function resolveMode(
  selected: HarnessMeta | undefined,
  chosen: HarnessPrefs | undefined,
  harnessDefaults: Seed | undefined,
  userConfig: UserConfig | null,
) {
  // Only an expressed preference is sent; otherwise the harness's own
  // configured default wins.
  const modes = selected?.permissionModes ?? [];
  const preferredMode = chosen?.mode ?? unsavedMode(selected, harnessDefaults, userConfig);
  const mode = modes.some((m) => m.id === preferredMode) ? preferredMode : "";
  const displayModeId = mode || (modes.find((m) => m.default)?.id ?? modes[0]?.id ?? "");
  const modeMeta = modes.find((m) => m.id === displayModeId);
  return { modes, preferredMode, mode, displayModeId, modeMeta };
}

/**
 * Who does the work: harness, account, model, effort, permissions. Each starts
 * on what this project last used, then the project's seed, then the defaults
 * from settings, and every pick is remembered for the project at once.
 */
export function useAgentChoice(sources: AgentSources, { patch }: Pick<RememberedChoices, "patch">) {
  const { project, remembered, harnesses, userConfig } = sources;
  const { instances, instance, harnessId } = resolveHarness(sources);
  const selected = harnesses.find((h) => h.id === harnessId);
  const chosen = remembered?.byHarness[harnessId];
  const harnessDefaults = project?.defaults.harnesses?.[harnessId];
  const models = resolveModel(instance, chosen, harnessDefaults, userConfig);
  const modes = resolveMode(selected, chosen, harnessDefaults, userConfig);

  // Catalogue validation affects what we send, not the preference we keep.
  const currentPrefs: HarnessPrefs = {
    instance: instance?.id ?? "",
    model: chosen?.model ?? models.model,
    mode: modes.preferredMode,
    effort: models.preferredEffort,
    want1m: models.want1m,
    fast: models.wantFast,
  };
  const remember = (harness: string, change: Partial<HarnessPrefs>) => {
    if (!project) return;
    patch(project.id, harness, change, harness === harnessId ? currentPrefs : undefined);
  };
  const selectModel = (next: ModelSelection) => {
    const previous = remembered?.byHarness[next.harness];
    const seed = project?.defaults.harnesses?.[next.harness];
    remember(next.harness, {
      instance: next.instance,
      model: next.model,
      mode: previous?.mode ?? seed?.mode ?? "",
      effort: previous?.effort ?? seed?.effort ?? "",
      want1m: previous?.want1m ?? false,
      fast: previous?.fast ?? false,
    });
  };

  return {
    instances,
    instance,
    harnessId,
    selected,
    ...models,
    ...modes,
    remember: (change: Partial<HarnessPrefs>) => remember(harnessId, change),
    switchModel: (model: string) =>
      selectModel({ harness: harnessId, instance: instance?.id ?? "", model }),
    pickInstance: (target: PickerInstance) => {
      if (target.id === instance?.id) return;
      const previous = remembered?.byHarness[target.driver];
      const wanted = previous?.model ?? project?.defaults.harnesses?.[target.driver]?.model;
      const restored = target.models.find((m) => m.id === wanted) ?? defaultModel(target);
      selectModel({ harness: target.driver, instance: target.id, model: restored?.id ?? "" });
    },
    switchAccount: (id: string, model: string) => {
      const target = instances.find((i) => i.id === id);
      if (target) selectModel({ harness: target.driver, instance: id, model });
    },
  };
}

/** What a thread is created with to run one model, and what to call it. */
export interface CompareAgent {
  harness: string;
  instance: string;
  /** As sent: with the "[1m]" window tag when the 1M window is on. */
  model: string;
  mode: string;
  effort: string;
  serviceTier: string;
  /** The model's name in the catalogue, for titles and toasts. */
  label: string;
}

/**
 * The agent for a model added to compare: the model picked, and the rest the
 * way the draft would resolve it on switching to that model's harness — the
 * effort, mode, 1M window and Fast this project last used there.
 */
export function compareAgent(sources: AgentSources, pick: ModelSelection): CompareAgent {
  const { project, remembered, harnesses, userConfig } = sources;
  const instance = pickerInstances(harnesses).find((i) => i.id === pick.instance);
  const harnessId = instance?.driver ?? pick.harness;
  const chosen = remembered?.byHarness[harnessId];
  const harnessDefaults = project?.defaults.harnesses?.[harnessId];
  const models = resolveModel(instance, { ...chosen, model: pick.model }, harnessDefaults, userConfig);
  const selected = harnesses.find((h) => h.id === harnessId);
  const { mode } = resolveMode(selected, chosen, harnessDefaults, userConfig);
  return {
    harness: harnessId,
    instance: instance?.id ?? pick.instance,
    model: models.effectiveModel,
    mode,
    effort: models.effort,
    serviceTier: models.serviceTier,
    label: instance?.models.find((m) => m.id === models.model)?.label || pick.model,
  };
}

/**
 * A thread's agent as the choices a draft in its project opens on. A thread
 * records the model as it ran, which can be the concrete model under an alias
 * or carry the "[1m]" window tag, so it is matched back to the catalogue's id
 * the way the thread's own picker does.
 */
export function threadChoices(
  agent: ThreadAgent,
  project: Project | undefined,
  harnesses: HarnessMeta[],
  userConfig: UserConfig | null,
): HarnessPrefs {
  const instance = resolveInstance(pickerInstances(harnesses), agent.instance, agent.harness);
  const selected = harnesses.find((h) => h.id === agent.harness);
  return {
    instance: instance?.id ?? agent.instance,
    // An empty model was the harness's default, and stays that.
    model: agent.model ? (matchModel(instance, agent.model)?.id ?? "") : "",
    mode: unsavedMode(selected, project?.defaults.harnesses?.[agent.harness], userConfig),
    effort: agent.effort,
    want1m: agent.model.endsWith("[1m]"),
    fast: agent.serviceTier !== "",
  };
}
