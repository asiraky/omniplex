const minute = 60_000;
export const day = 24 * 60 * minute;

export function localDateTime(at: number, timeZone: string): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(at);
  const value = (type: string) => parts.find((p) => p.type === type)!.value;
  return `${value("year")}-${value("month")}-${value("day")}T${value("hour")}:${value("minute")}`;
}

// Find actual instants for a wall-clock minute. A DST gap has none; a fold
// has two. Never let Date silently normalize a nonexistent or repeated time.
export function absoluteCandidates(value: string, timeZone: string): number[] {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return [];
  const wall = Date.parse(`${value}:00Z`);
  if (!Number.isFinite(wall)) return [];
  const offsets = new Set<number>();
  for (let hours = -36; hours <= 36; hours += 6) {
    const instant = wall + hours * 60 * minute;
    offsets.add(
      Date.parse(`${localDateTime(instant, timeZone)}:00Z`) - instant,
    );
  }
  return [...offsets]
    .map((offset) => wall - offset)
    .filter((at) => localDateTime(at, timeZone) === value)
    .sort((a, b) => a - b);
}
export function scheduleLabel(at: number, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone,
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "shortOffset",
  }).format(at);
}
export function validateSchedule(at: number, now: number): string | undefined {
  if (!Number.isFinite(at) || at <= now || at - now > day)
    return "Choose a time in the next 24 hours.";
}
