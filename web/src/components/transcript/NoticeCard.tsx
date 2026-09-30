import { ArchiveIcon, ArrowRightLeftIcon } from "lucide-react";

import { fmtTokens } from "~/lib/format";
import type { Item } from "~/protocol";

// A compaction boundary, as one quiet centered line in the flow. The harness
// compressed the conversation to reclaim window; the reader mostly needs to
// know it happened and roughly how much it recovered.
export function NoticeCard({ item }: { item: Item }) {
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
