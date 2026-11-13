/** One field of an elicitation schema: JSON Schema plus the bridge's `x-` hints. */
export type ElicitationField = Record<string, any>;

/**
 * What the human has put into one field so far. A choice field keeps the
 * options it has picked and its free-text "Other" apart, so going back to a
 * question shows it as it was left rather than as the answer it reduced to.
 */
export interface FieldDraft {
  /** A text, number or boolean field's value. */
  value: unknown;
  /** The options picked, in the order they were picked: at most one for a single-select. */
  picked: string[];
  /** The free-text "Other" is chosen. */
  otherOn: boolean;
  otherText: string;
}

/** A half-answered elicitation: the question on screen, and every field's draft. */
export interface ElicitationDraft {
  step: number;
  fields: Record<string, FieldDraft>;
}

export function isMultiSelect(field: ElicitationField): boolean {
  return Boolean(field["x-multiSelect"]);
}

/** A choice field's options, in order; none for any other field. */
export function optionsOf(field: ElicitationField): string[] {
  return Array.isArray(field.enum) ? (field.enum as string[]) : [];
}

/** A choice field offers a free-text "Other" after its options. */
export function allowsOther(field: ElicitationField): boolean {
  return Boolean(field.enum) && Boolean(field["x-allowOther"]);
}

export function initialDraft(properties: Record<string, ElicitationField>): ElicitationDraft {
  const fields: Record<string, FieldDraft> = {};
  for (const [key, field] of Object.entries(properties)) {
    const d = field.default;
    const picked = !field.enum
      ? []
      : isMultiSelect(field)
        ? Array.isArray(d)
          ? d.filter((v): v is string => typeof v === "string")
          : []
        : typeof d === "string"
          ? [d]
          : [];
    fields[key] = { value: d ?? "", picked, otherOn: false, otherText: "" };
  }
  return { step: 0, fields };
}

/**
 * Picks an option. A single-select replaces its pick and drops "Other"; a
 * multi-select toggles it and keeps whatever else is chosen.
 */
export function pickOption(field: ElicitationField, d: FieldDraft, option: string): FieldDraft {
  if (!isMultiSelect(field)) return { ...d, picked: [option], otherOn: false };
  const picked = d.picked.includes(option)
    ? d.picked.filter((o) => o !== option)
    : [...d.picked, option];
  return { ...d, picked };
}

/** Chooses "Other". A single-select gives up its pick for it; a multi-select toggles it. */
export function pickOther(field: ElicitationField, d: FieldDraft): FieldDraft {
  if (!isMultiSelect(field)) return { ...d, picked: [], otherOn: true };
  return { ...d, otherOn: !d.otherOn };
}

/**
 * The answer a field sends: a single-select's label or its "Other" text, a
 * multi-select's labels with any "Other" text last, or a plain field's value.
 */
export function answerOf(field: ElicitationField, d: FieldDraft): unknown {
  if (!field.enum) return d.value;
  const other = d.otherText.trim();
  if (isMultiSelect(field)) return d.otherOn && other ? [...d.picked, other] : d.picked;
  return d.otherOn ? other : (d.picked[0] ?? "");
}

export function answersOf(
  properties: Record<string, ElicitationField>,
  draft: ElicitationDraft,
): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(properties).map(([key, field]) => [key, answerOf(field, draft.fields[key])]),
  );
}

/**
 * Half-typed answers, by request, in memory only. Switching to another thread
 * unmounts the prompt; coming back finds the answers where they were left. A
 * reload starts over, which is all this is meant to cover.
 *
 * A draft outlives the click that answers it: the answer can fail to send on
 * a bad connection, and the question stays. It goes when the thread stops
 * listing the request as pending, however that happened: answered here or on
 * another device, cancelled, or dropped with its turn.
 */
const drafts = new Map<string, { threadId: string; draft: ElicitationDraft }>();

export const elicitationDrafts = {
  get: (requestId: string) => drafts.get(requestId)?.draft,
  set: (threadId: string, requestId: string, draft: ElicitationDraft) =>
    void drafts.set(requestId, { threadId, draft }),
  /** Drops the thread's drafts for requests it no longer has pending. */
  keepOnly(threadId: string, pending: readonly string[]) {
    const live = new Set(pending);
    for (const [requestId, entry] of drafts) {
      if (entry.threadId === threadId && !live.has(requestId)) drafts.delete(requestId);
    }
  },
};
