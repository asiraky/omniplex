import { useEffect, useState } from "react";
import type { ScheduledPrompt } from "~/protocol";
import { Button } from "~/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";
import {
  absoluteCandidates,
  localDateTime,
  scheduleLabel,
  validateSchedule,
  zoneOr,
} from "~/lib/scheduleTime";

export type ScheduleInput = { text: string; dueAt: number; timeZone: string };

export function ScheduleDialog({
  initialText,
  imageCount = 0,
  schedule,
  onClose,
  onSave,
}: {
  initialText: string;
  imageCount?: number;
  schedule?: ScheduledPrompt;
  onClose: () => void;
  onSave: (input: ScheduleInput) => Promise<void>;
}) {
  const [text, setText] = useState(initialText);
  const [kind, setKind] = useState<"relative" | "absolute">(
    schedule ? "absolute" : "relative",
  );
  const [zone, setZone] = useState(
    schedule?.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone,
  );
  const [hours, setHours] = useState("1");
  const [minutes, setMinutes] = useState("0");
  const [wall, setWall] = useState(
    localDateTime(schedule?.dueAt ?? Date.now() + 3_600_000, zone),
  );
  const [fold, setFold] = useState("");
  const [now, setNow] = useState(Date.now());
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState("");
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  let candidates: number[] = [];
  let zoneError = "";
  try {
    candidates = absoluteCandidates(wall, zone);
  } catch {
    zoneError = "Choose a valid IANA timezone, such as Australia/Brisbane.";
  }
  const duration = (Number(hours) * 60 + Number(minutes)) * 60_000;
  const dueAt =
    kind === "relative"
      ? now + duration
      : candidates.length === 1
        ? candidates[0]
        : Number(fold) || NaN;
  const error =
    zoneError ||
    (kind === "relative" && (Number(hours) < 0 || Number(minutes) < 0)
      ? "Enter a positive duration."
      : "") ||
    (kind === "absolute" && candidates.length === 0
      ? "This local time does not exist. Choose another time."
      : "") ||
    (kind === "absolute" && candidates.length > 1 && !fold
      ? "This time occurs twice. Choose which one."
      : "") ||
    validateSchedule(dueAt, now);
  const field =
    "h-11 w-full min-w-0 rounded-md border bg-background px-3 text-sm";
  async function save() {
    if (error || saving) return;
    const at = kind === "relative" ? Date.now() + duration : dueAt;
    const invalid = validateSchedule(at, Date.now());
    if (invalid) {
      setFailure(invalid);
      return;
    }
    setSaving(true);
    setFailure("");
    try {
      await onSave({ text, dueAt: at, timeZone: zone });
      onClose();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
      setSaving(false);
    }
  }
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !saving) onClose();
      }}
    >
      <SheetContent
        side="bottom"
        className="mx-auto max-h-[90dvh] max-w-lg overflow-y-auto rounded-t-xl p-4 pb-6"
        aria-describedby={undefined}
      >
        <SheetHeader className="p-0">
          <SheetTitle>
            {schedule ? "Edit scheduled message" : "Schedule message"}
          </SheetTitle>
        </SheetHeader>
        <fieldset disabled={saving} className="contents">
          <label className="grid gap-1 text-sm">
            Message
            <textarea
              aria-label="Scheduled message"
              className="min-h-24 w-full rounded-md border bg-background p-3"
              value={text}
              onChange={(e) => setText(e.target.value)}
              disabled={saving}
            />
          </label>
          {!!imageCount && (
            <p className="text-xs text-muted-foreground">
              {imageCount} attached image(s) included
            </p>
          )}
          <div className="flex gap-2">
            <Button
              variant={kind === "relative" ? "default" : "outline"}
              onClick={() => setKind("relative")}
            >
              In…
            </Button>
            <Button
              variant={kind === "absolute" ? "default" : "outline"}
              onClick={() => setKind("absolute")}
            >
              At a time
            </Button>
          </div>
          {kind === "relative" ? (
            <>
              <div className="grid grid-cols-2 gap-3">
                <label className="grid gap-1 text-sm">
                  Hours
                  <input
                    className={field}
                    type="number"
                    min="0"
                    max="24"
                    value={hours}
                    onChange={(e) => setHours(e.target.value)}
                  />
                </label>
                <label className="grid gap-1 text-sm">
                  Minutes
                  <input
                    className={field}
                    type="number"
                    min="0"
                    max="1440"
                    value={minutes}
                    onChange={(e) => setMinutes(e.target.value)}
                  />
                </label>
              </div>
              <div className="flex flex-wrap gap-2">
                {[30, 60, 120].map((n) => (
                  <Button
                    key={n}
                    variant="outline"
                    onClick={() => {
                      setHours(String(Math.floor(n / 60)));
                      setMinutes(String(n % 60));
                    }}
                  >
                    {n < 60
                      ? `${n} minutes`
                      : `${n / 60} hour${n > 60 ? "s" : ""}`}
                  </Button>
                ))}
              </div>
            </>
          ) : (
            <label className="grid min-w-0 gap-1 text-sm">
              Date and time
              <input
                aria-label="Date and time"
                className={field}
                type="datetime-local"
                value={wall}
                onChange={(e) => {
                  setWall(e.target.value);
                  setFold("");
                }}
              />
            </label>
          )}
          <label className="grid gap-1 text-sm">
            Timezone
            <input
              className={field}
              value={zone}
              onChange={(e) => {
                setZone(e.target.value);
                setFold("");
              }}
              placeholder="Australia/Brisbane"
              autoCapitalize="none"
              spellCheck={false}
            />
          </label>
          {kind === "absolute" && candidates.length > 1 && (
            <label className="grid gap-1 text-sm">
              Which occurrence?
              <select
                className={field}
                value={fold}
                onChange={(e) => setFold(e.target.value)}
              >
                <option value="">Choose an occurrence</option>
                {candidates.map((at) => (
                  <option key={at} value={at}>
                    {scheduleLabel(at, zone)}
                  </option>
                ))}
              </select>
            </label>
          )}
          {!error && (
            <p className="text-sm">
              Sends {scheduleLabel(dueAt, zone)}
              <br />
              <span className="text-muted-foreground">{zone}</span>
            </p>
          )}
          {(error || failure) && (
            <p role="alert" className="text-sm text-destructive">
              {failure || error}
            </p>
          )}
          <p className="text-xs text-muted-foreground">
            Your phone can be closed. The host must be awake. If offline, it
            catches up within one hour; later messages are marked missed. Busy
            sessions wait until free.
          </p>
          <Button
            disabled={saving || !!error || (!text.trim() && !imageCount)}
            onClick={() => void save()}
          >
            {saving
              ? "Saving…"
              : schedule
                ? "Save changes"
                : "Schedule message"}
          </Button>
        </fieldset>
      </SheetContent>
    </Sheet>
  );
}

export function ScheduledPrompts({
  schedules,
  disabled,
  onEdit,
  onAction,
}: {
  schedules: ScheduledPrompt[];
  disabled?: boolean;
  onEdit: (p: ScheduledPrompt) => void;
  onAction: (action: string, p: ScheduledPrompt) => Promise<void>;
}) {
  const [, setNow] = useState(Date.now());
  const [pending, setPending] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, []);
  // A resume the server armed is not a message anybody scheduled: there is no
  // text they wrote, nothing to edit, and the failure card in the transcript
  // already says when it is coming and offers both overrides. Repeating it
  // here was two boxes for one fact, above a composer that is short of room
  // on a phone.
  const visible = schedules
    .filter(
      (p) => p.kind !== "resume" && p.status !== "sent" && p.status !== "cancelled",
    )
    .sort((a, b) => a.dueAt - b.dueAt);
  if (!visible.length) return null;
  async function action(name: string, p: ScheduledPrompt) {
    setPending(p.id);
    setError("");
    try {
      await onAction(name, p);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setPending("");
    }
  }
  return (
    <div
      className="mx-auto max-h-[28dvh] w-full max-w-3xl overflow-y-auto px-3 pt-2"
      aria-label="Scheduled messages"
    >
      {visible.map((p) => (
        <div
          key={p.id}
          data-schedule-id={p.id}
          className="mb-2 rounded-xl border bg-background p-3 text-sm shadow-sm"
        >
          <div className="font-medium">
            ◷{" "}
            {p.status === "missed"
              ? "Missed"
              : p.status === "failed"
                ? "Failed"
                : p.status === "ready"
                  ? "Due — waiting for session"
                  : "Scheduled"}{" "}
            · {scheduleLabel(p.dueAt, p.timeZone)}
          </div>
          <div className="text-xs text-muted-foreground">
            {zoneOr(p.timeZone)}
            {p.status === "pending" && p.dueAt > Date.now()
              ? ` · in ${Math.ceil((p.dueAt - Date.now()) / 60_000)} min`
              : ""}{" "}
            · {p.model || "Default model"}
            {p.effort ? ` · ${p.effort}` : ""}
          </div>
          <p className="mt-2 line-clamp-3 whitespace-pre-wrap break-words">{p.prompt}</p>
          {!!p.images?.length && (
            <p className="text-xs">{p.images.length} image(s) attached</p>
          )}
          {p.error && (
            <p className="mt-1 text-xs text-destructive">{p.error}</p>
          )}
          <div className="mt-1 flex flex-wrap gap-1">
            <Button
              variant="ghost"
              disabled={disabled || !!pending}
              onClick={() => onEdit(p)}
            >
              {p.status === "missed" || p.status === "failed"
                ? "Reschedule"
                : "Edit"}
            </Button>
            <Button
              variant="ghost"
              disabled={disabled || !!pending}
              onClick={() => void action("send_schedule", p)}
            >
              {p.status === "failed" ? "Retry now" : "Send now"}
            </Button>
            <Button
              variant="ghost"
              disabled={disabled || !!pending}
              onClick={() => void action("cancel_schedule", p)}
            >
              Cancel
            </Button>
          </div>
        </div>
      ))}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
