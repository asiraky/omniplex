import { ArchiveIcon, ArrowRightLeftIcon, SparklesIcon } from "lucide-react";

import { fmtTokens } from "~/lib/format";
import type { Item } from "~/protocol";

// A system event as one quiet centered line in the flow: a switch of model or
// effort, of account, or a compaction boundary. For a compaction the reader
// mostly needs to know it happened and roughly how much it recovered.
export function NoticeCard({
  item,
  describeSettings,
}: {
  item: Item;
  /** Names a model and effort for a model notice, through the thread's
      catalogue. Absent until the catalogue has loaded. */
  describeSettings?: (model: string, effort: string) => string;
}) {
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
