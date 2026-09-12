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
/**
 * The zone to render a schedule in. A human's schedule carries the zone they
 * wrote it in; one the server armed itself carries none, and belongs in the
 * reader's own. Anything Intl refuses — a zone from an older build, or Go's
 * "Local" — falls back the same way rather than throwing: a schedule shown in
 * the wrong zone is a nuisance, a schedule that takes the list down with it is
 * not.
 */
export function zoneOr(timeZone: string | undefined): string {
  const here = Intl.DateTimeFormat().resolvedOptions().timeZone;
  if (!timeZone) return here;
  try {
    new Intl.DateTimeFormat(undefined, { timeZone }).format(0);
    return timeZone;
  } catch {
    return here;
  }
}

export function scheduleLabel(at: number, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone: zoneOr(timeZone),
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "shortOffset",
  }).format(at);
}
// waitLabel is a moment stated the way somebody glancing at a phone reads it:
// the clock time in their own zone, and how long that is from now, because
// "11:10 am" alone does not say whether that is ten minutes or ten hours away.
// The zone is the device's on purpose — the reader may well be in a different
// one from the machine the session runs on.
export function waitLabel(at: number, now: number): string {
  const clock = new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
    // Only worth naming a day when it is not this one.
    ...(new Date(at).toDateString() === new Date(now).toDateString()
      ? {}
      : { weekday: "short" }),
  }).format(at);
  const left = at - now;
  if (left <= minute) return `${clock} (any moment now)`;
  const mins = Math.round(left / minute);
  if (mins < 60) return `${clock} (in ${mins} min)`;
  const hours = Math.floor(mins / 60);
  const rest = mins % 60;
  return `${clock} (in ${hours}h${rest ? ` ${rest}m` : ""})`;
}

export function validateSchedule(at: number, now: number): string | undefined {
  if (!Number.isFinite(at) || at <= now || at - now > day)
    return "Choose a time in the next 24 hours.";
}
