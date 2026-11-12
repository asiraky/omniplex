/**
 * The other accounts signed in to the same login, by display name, falling
 * back to the id. Two such accounts share one set of limits and one history,
 * which is what made two rows on the Usage page look like mixed-up data.
 */
export function sameLoginNames(ids: string[] | undefined, nameOf: (id: string) => string | undefined): string[] {
  return (ids ?? []).map((id) => nameOf(id) || id);
}

/** "a", "a and b", "a, b and c". */
export function joinAnd(names: string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}
