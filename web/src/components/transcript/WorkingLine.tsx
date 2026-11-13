import { BrainIcon, TimerIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { formatDuration } from "~/rows";

import { thoughtPreview, type LiveActivity } from "./liveActivity";

// "0s" for the first second rather than formatDuration's floor of "1s": a
// timer that starts on 1 has skipped a beat the reader just watched.
function elapsed(ms: number): string {
  return ms < 1000 ? "0s" : formatDuration(Math.floor(ms / 1000) * 1000);
}

/**
 * How long the turn has been running, ticking once a second. Worked out from
 * the real time each tick, so a phone that froze the timer in a background tab
 * shows the right figure on its first tick back rather than resuming the count.
 * The start is the server's clock and the time is this device's; a skewed
 * device clock can put the start in the future, which reads as 0s.
 */
function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);
  return <span className="tabular-nums">{elapsed(Math.max(0, now - since))}</span>;
}

/**
 * The line at the tail of a running turn: what the agent is doing, and for
 * how long. Thinking shimmers, because nothing else on screen is moving then;
 * while a call runs its row shimmers, and while text streams the caret moves,
 * so this line stays still and only keeps time. Reasoning that streams in is previewed under
 * it, clamped to two lines on a phone and three wider up.
 *
 * Not a live region: the timer changes every second, and announcing it would
 * drown out everything else a screen reader has to say.
 */
export function WorkingLine({ activity }: { activity: LiveActivity }) {
  const thinking = activity.state === "thinking";
  const preview = activity.thought ? thoughtPreview(activity.thought.text ?? "") : "";
  const Icon = thinking ? BrainIcon : TimerIcon;

  return (
    <div data-working-line className="text-muted-foreground flex min-w-0 flex-col gap-1 text-[13px]">
      <div className="flex min-w-0 items-center gap-2">
        <Icon aria-hidden className="size-3.5 shrink-0" />
        <span className={thinking ? "shimmer" : undefined}>{thinking ? "Thinking" : "Working"}</span>
        {activity.startedAt !== undefined && (
          <>
            <span aria-hidden>·</span>
            <Elapsed since={activity.startedAt} />
          </>
        )}
      </div>
      {preview && (
        <p className="text-thought line-clamp-2 border-l-2 pl-3 text-[12px] leading-relaxed break-words italic md:line-clamp-3">
          {preview}
        </p>
      )}
    </div>
  );
}
