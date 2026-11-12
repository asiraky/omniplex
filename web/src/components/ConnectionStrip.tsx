import type { ConnectionStatus } from "~/client";
import { cn } from "~/lib/utils";

import { Spinner } from "./ui/spinner";

/**
 * "Reconnecting…" floating at the top of the thread while the socket is down.
 * It overlays the transcript rather than taking a row, so nothing reflows when
 * it comes and goes; on a flaky connection a strip that pushed the transcript
 * down would make it jump every time. It is always mounted and only fades:
 * in after a beat, so a reconnect that lands within a second never flashes it,
 * and out at once, since being back online is the news.
 */
export function ConnectionStrip({ status }: { status: ConnectionStatus }) {
  const online = status === "online";
  return (
    <div
      role="status"
      aria-live="polite"
      aria-hidden={online || undefined}
      className={cn(
        "pointer-events-none absolute top-2 right-(--scrollbar-w,0px) left-0 z-20 flex justify-center px-3 transition-opacity duration-200",
        online ? "opacity-0 delay-0" : "opacity-100 delay-700",
      )}
    >
      <div className="bg-card/90 text-muted-foreground flex items-center gap-2 rounded-full border px-3 py-1 text-[12px] shadow-sm backdrop-blur">
        {/* The spin lives on the icon's children (see .spin-icon), so that is
            where reduced motion has to stop it. */}
        <Spinner aria-hidden role={undefined} className="size-3.5 motion-reduce:*:animate-none!" />
        <span>Reconnecting…</span>
      </div>
    </div>
  );
}
