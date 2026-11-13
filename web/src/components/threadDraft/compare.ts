import type { ModelSelection } from "~/components/ModelPicker";

/** Models added beside the draft's own: four threads at most. */
export const MAX_COMPARE = 3;

/** The draft's prompt as it starts a compare title. */
const TITLE_PROMPT_RUNES = 50;

export const sameModel = (a: ModelSelection, b: ModelSelection) =>
  a.instance === b.instance && a.model === b.model;

/**
 * The compare set with one more model, or the set unchanged when that model
 * is already in it, is the draft's own, or the set is full.
 */
export function addCompare(
  set: ModelSelection[],
  primary: ModelSelection,
  next: ModelSelection,
): ModelSelection[] {
  if (set.length >= MAX_COMPARE) return set;
  if (sameModel(next, primary) || set.some((m) => sameModel(m, next))) return set;
  return [...set, next];
}

// Dash-separated and lowercase, like the branch template's slugs.
function slug(text: string): string {
  return text
    .toLowerCase()
    .replace(/\[1m\]$/, "")
    .replace(/[^a-z0-9.]+/g, "-")
    .replace(/^[-.]+|[-.]+$/g, "");
}

/**
 * The branch each compare thread is created on, in the order of `models`.
 * A branch the person typed is shared out with each model's id on the end, so
 * the worktrees sit side by side; two accounts running the same model get a
 * number to keep them apart. No branch typed leaves every one empty, for the
 * server's own per-thread name.
 */
export function compareBranches(branch: string, models: string[]): string[] {
  const base = branch.trim();
  if (!base) return models.map(() => "");
  const taken = new Set<string>();
  return models.map((model) => {
    const stem = `${base}-${slug(model) || "model"}`;
    let name = stem;
    for (let n = 2; taken.has(name); n++) name = `${stem}-${n}`;
    taken.add(name);
    return name;
  });
}

/**
 * Each model's name for its title and toasts, with the account after it when
 * another model in the set has the same name: the same model on two accounts
 * would otherwise read as one.
 */
export function compareLabels(models: { label: string; account: string }[]): string[] {
  return models.map(({ label, account }) =>
    account && models.filter((m) => m.label === label).length > 1 ? `${label} (${account})` : label,
  );
}

/**
 * A compare thread's title: the start of the prompt and the model, so rows
 * that would otherwise read the same tell one model from another.
 */
export function compareTitle(prompt: string, label: string): string {
  const flat = prompt.split(/\s+/).filter(Boolean).join(" ");
  const runes = Array.from(flat);
  const head =
    runes.length > TITLE_PROMPT_RUNES
      ? `${runes.slice(0, TITLE_PROMPT_RUNES).join("").trimEnd()}…`
      : flat;
  return head ? `${head} · ${label}` : label;
}
