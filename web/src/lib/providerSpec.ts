// The write shape for provider-instance CRUD, built here so the forms stay
// dumb. Everything security-sensitive about the spec lives in one function:
// which env vars travel at all, and when a secret's value is included versus
// the keep-what-is-stored marker.

import type { ProviderConfigField, ProviderEnvVar, ProviderInstanceSpec } from "~/protocol";

/**
 * Derives the instance id from the display name the user typed. The id is the
 * routing key sessions record forever, so it is a slug rather than free text —
 * and once taken it gets a numeric suffix instead of colliding, because two
 * accounts both called "Work" are still two accounts.
 */
export function deriveInstanceId(name: string, taken: Iterable<string>): string {
  const base =
    name
      .toLowerCase()
      // Fold accents away rather than dropping the letters that carry them.
      .normalize("NFKD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "instance";
  const used = new Set(taken);
  if (!used.has(base)) return base;
  for (let n = 2; ; n++) {
    const candidate = `${base}-${n}`;
    if (!used.has(candidate)) return candidate;
  }
}

/** True when the stored env holds a secret under this name. */
export function hasStoredSecret(stored: ProviderEnvVar[] | undefined, name: string): boolean {
  return (stored ?? []).some((v) => v.name === name && v.sensitive);
}

/**
 * The form's starting values: every plain stored value the server echoed for
 * a schema field, so an edit begins from what is actually configured rather
 * than from blank. Secrets are never echoed and never prefilled — their
 * "(unchanged)" state is the placeholder, not a value.
 */
export function initialFieldValues(
  fields: ProviderConfigField[],
  stored: ProviderEnvVar[] | undefined,
): Record<string, string> {
  const values: Record<string, string> = {};
  for (const field of fields) {
    if (field.kind === "secret") continue;
    const v = (stored ?? []).find((s) => s.name === field.env && !s.sensitive)?.value;
    if (v != null) values[field.env] = v;
  }
  return values;
}

/**
 * Builds the spec for add/save_provider_instance by merging the form over the
 * env the server echoed (`stored` — plain values verbatim, secrets redacted to
 * bare sensitive names). Save replaces env wholesale server-side, so the merge
 * is what makes a rename or an enabled-toggle lossless:
 *
 * - a secret the user typed travels with its value, marked sensitive — this
 *   is the one place a secret value enters a frame, and it must never appear
 *   anywhere else (not in logs, not in state, not in errors);
 * - a secret left blank where one is stored becomes the valueless sensitive
 *   marker, which the server reads as "keep the stored secret";
 * - a secret left blank with nothing stored is simply not configured;
 * - any other schema field travels with the form's text — which was prefilled
 *   from `stored`, so blank genuinely means the user cleared it;
 * - stored vars with no schema field (hand-authored config) carry through
 *   untouched, keep-markers for the sensitive ones.
 */
export function buildInstanceSpec(opts: {
  id: string;
  driver: string;
  displayName: string;
  enabled: boolean;
  fields: ProviderConfigField[];
  /** What the form holds, keyed by env var name; see initialFieldValues. */
  values: Record<string, string>;
  /** The instance's echoed env (meta.env); absent for a brand-new instance. */
  stored?: ProviderEnvVar[];
}): ProviderInstanceSpec {
  const env: ProviderEnvVar[] = [];
  for (const field of opts.fields) {
    const typed = (opts.values[field.env] ?? "").trim();
    if (field.kind === "secret") {
      if (typed) env.push({ name: field.env, value: typed, sensitive: true });
      else if (hasStoredSecret(opts.stored, field.env)) env.push({ name: field.env, sensitive: true });
    } else if (typed) {
      env.push({ name: field.env, value: typed });
    }
  }
  // Whatever else the config holds survives the edit verbatim: an env var
  // someone wrote by hand must not vanish because this UI never heard of it.
  for (const v of opts.stored ?? []) {
    if (opts.fields.some((f) => f.env === v.name)) continue;
    env.push(v.sensitive ? { name: v.name, sensitive: true } : { name: v.name, value: v.value });
  }
  return {
    id: opts.id,
    driver: opts.driver,
    displayName: opts.displayName.trim() || opts.id,
    env,
    enabled: opts.enabled,
  };
}
