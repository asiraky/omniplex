import {
  ArchiveIcon,
  ArrowRightLeftIcon,
  OctagonXIcon,
  SparklesIcon,
  TriangleAlertIcon,
} from "lucide-react";

import { fmtTokens } from "~/lib/format";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";

// A system event as one quiet centered line in the flow: a switch of model or
// effort, of account, a compaction boundary, or something a harness extension
// warned about. The reader mostly needs to know it happened, not to stop and
// read it.
export function NoticeCard({
  item,
  describeSettings,
}: {
  item: Item;
  /** Names a model and effort for a model notice, through the thread's
      catalogue. Absent until the catalogue has loaded. */
  describeSettings?: (model: string, effort: string) => string;
}) {
  if (item.noticeKind === "notify") return <NotifyNotice item={item} />;
  if (item.noticeKind === "model") {
    // Without the catalogue an alias and the id it resolves to read as two
    // models; drawing nothing until it loads beats a line for no switch.
    if (!describeSettings) return null;
    // Replies below this line came from these settings.
    const to = describeSettings(item.model ?? "", item.effort ?? "");
    // The log can name one model two ways, by an alias and by the id it
    // resolves to; read through the catalogue they say the same, and that
    // is no switch.
    if (item.from && describeSettings(item.from.model, item.from.effort) === to) return null;
    return (
      <div className="fade-in flex justify-center">
        <div className="text-muted-foreground flex min-w-0 items-center gap-1.5 rounded-full border px-3 py-1 text-[12px]">
          <SparklesIcon className="size-3.5 shrink-0" />
          <span className="truncate">
            Switched to {to}
          </span>
        </div>
      </div>
    );
  }
  if (item.noticeKind === "account") {
    return (
      <div className="fade-in flex justify-center">
        <div className="text-muted-foreground flex items-center gap-1.5 rounded-full border px-3 py-1 text-[12px]">
          <ArrowRightLeftIcon className="size-3.5 shrink-0" />
          <span>Switched to {item.title}</span>
        </div>
      </div>
    );
  }
  const detail =
    item.preTokens && item.postTokens
      ? `${fmtTokens(item.preTokens)} → ${fmtTokens(item.postTokens)}`
      : "";
  return (
    <div className="fade-in flex justify-center">
      <div className="text-muted-foreground flex items-center gap-1.5 rounded-full border px-3 py-1 text-[12px]">
        <ArchiveIcon className="size-3.5 shrink-0" />
        <span>
          {item.trigger === "manual" ? "Context compacted" : "Context auto-compacted"}
          {detail && ` — ${detail}`}
        </span>
      </div>
    </div>
  );
}

// An extension's message is its own words at its own length, so unlike the
// fixed phrases above it has to wrap: a pill would overflow a phone. Only the
// icon carries the level: this is the record, and the toast was the alarm.
function NotifyNotice({ item }: { item: Item }) {
  const error = item.level === "error";
  const Icon = error ? OctagonXIcon : TriangleAlertIcon;
  return (
    <div className="fade-in flex justify-center">
      <div className="text-muted-foreground flex max-w-full items-start gap-1.5 rounded-lg border px-3 py-1 text-[12px]">
        <Icon className={cn("mt-0.5 size-3.5 shrink-0", error ? "text-destructive" : "text-attention")} />
        <span className="min-w-0 break-words whitespace-pre-wrap">
          <span className="sr-only">{error ? "Error: " : "Warning: "}</span>
          {item.text}
        </span>
      </div>
    </div>
  );
}
