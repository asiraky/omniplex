import { useEffect, useState } from "react";
import type { ScheduledPrompt } from "~/protocol";
import { Button } from "~/components/ui/button";
import { scheduleLabel } from "~/lib/scheduleTime";

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
  const visible = schedules
    .filter((p) => p.status !== "sent" && p.status !== "cancelled")
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
                  ? "Due — waiting for thread"
                  : "Scheduled"}{" "}
            · {scheduleLabel(p.dueAt, p.timeZone)}
          </div>
          <div className="text-xs text-muted-foreground">
            {p.timeZone}
            {p.status === "pending" && p.dueAt > Date.now()
              ? ` · in ${Math.ceil((p.dueAt - Date.now()) / 60_000)} min`
              : ""}{" "}
            · {p.model || "Default model"}
            {p.effort ? ` · ${p.effort}` : ""}
          </div>
          <p className="mt-2 line-clamp-3 whitespace-pre-wrap break-words">
            {p.prompt}
          </p>
          {!!p.images?.length && (
            <p className="text-xs">{p.images.length} attachment(s)</p>
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
