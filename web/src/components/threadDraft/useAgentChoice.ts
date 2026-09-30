import type { ModelSelection } from "~/components/ModelPicker";
import { defaultModel, pickerInstances, resolveInstance, type PickerInstance } from "~/lib/models";
import { modeForLevel } from "~/lib/permissions";
import type { HarnessPrefs, ProjectPrefs } from "~/lib/threadPrefs";
import type { HarnessMeta, Project, ProjectDefaults, UserConfig } from "~/protocol";
import type { RememberedChoices } from "./useRememberedChoices";

/** A project's starting choices for one harness, from its settings. */
type Seed = NonNullable<ProjectDefaults["harnesses"]>[string];

interface Sources {
  project: Project | undefined;
  remembered: ProjectPrefs | undefined;
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
}

/** The harness and account the thread runs under. */
function resolveHarness({ project, remembered, harnesses, userConfig }: Sources) {
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

/** The model, its context window and its effort. */
function resolveModel(
  instance: PickerInstance | undefined,
  chosen: HarnessPrefs | undefined,
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
  return { model, want1m, supports1m, effectiveModel, effort, preferredEffort };
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
  const preferredMode =
    chosen?.mode ?? harnessDefaults?.mode ?? modeForLevel(modes, userConfig?.defaultLevel);
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
export function useAgentChoice(sources: Sources, { patch }: Pick<RememberedChoices, "patch">) {
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
