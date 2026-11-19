import {
  ArchiveIcon,
  ArrowRightLeftIcon,
  OctagonXIcon,
  RotateCwIcon,
  SparklesIcon,
  TriangleAlertIcon,
} from "lucide-react";

import { useAgentNav } from "~/lib/agentNav";
import { fmtTokens } from "~/lib/format";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";

// A system event as one quiet centered line in the flow: a switch of model or
// effort, of account, a restart, a compaction boundary, or something a harness
// extension warned about. The reader mostly needs to know it happened, not to stop and
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
  if (item.noticeKind === "restart") return <RestartNotice item={item} />;
  return <CompactionNotice item={item} />;
}

// A compaction boundary. On an agent's thread it is a row in the agent's log,
// so it carries the epoch and a link to that row, where the summary and what
// the next epoch started from can be read.
function CompactionNotice({ item }: { item: Item }) {
  const { current, threadId, openAgent } = useAgentNav();
  const detail =
    item.preTokens && item.postTokens
      ? `${fmtTokens(item.preTokens)} → ${fmtTokens(item.postTokens)}`
      : "";
  const epoch = item.epoch;
  return (
    <div className="fade-in flex justify-center">
      <div className="text-muted-foreground flex max-w-full flex-wrap items-center justify-center gap-x-1.5 gap-y-0.5 rounded-full border px-3 py-1 text-[12px]">
        <ArchiveIcon className="size-3.5 shrink-0" />
        <span>
          {item.trigger === "manual" ? "Context compacted" : "Context auto-compacted"}
          {detail && ` — ${detail}`}
        </span>
        {epoch !== undefined && <span className="font-mono text-[11px]">· epoch {epoch}</span>}
        {epoch !== undefined && current && (
          <button
            type="button"
            // The thread goes along: this one may be archived, and the
            // agent's live log has its own epoch of that number.
            onClick={() => openAgent(current.id, "log", { ...(threadId ? { thread: threadId } : {}), epoch })}
            className="text-primary hit-44 relative font-medium hover:underline"
          >
            Inspect
          </button>
        )}
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

// Where the harness came back up on the setup as it is now: replies below this
// line had the new system prompt, MCP servers and skills. A failed restart
// says why in the server's own words, so it wraps like an extension's message
// rather than overflowing a phone, and is the one notice drawn as an error:
// the thread is still on its old setup, which the reader has to know. On an
// agent's thread it names the prompt revision it came up on, and links to that
// revision's change when there is an earlier one to compare it with.
function RestartNotice({ item }: { item: Item }) {
  const { current, openAgent } = useAgentNav();
  if (!item.text) {
    const rev = item.promptRev;
    return (
      <div className="fade-in flex justify-center">
        <div className="text-muted-foreground flex max-w-full flex-wrap items-center justify-center gap-x-1.5 gap-y-0.5 rounded-full border px-3 py-1 text-[12px]">
          <RotateCwIcon className="size-3.5 shrink-0" />
          <span>
            Thread restarted{rev !== undefined && <>, prompt <span className="font-mono text-[11px]">r{rev}</span></>}
          </span>
          {rev !== undefined && rev > 1 && current && (
            <button
              type="button"
              onClick={() => openAgent(current.id, "prompt", { rev })}
              className="text-primary hit-44 relative font-medium hover:underline"
            >
              View change
            </button>
          )}
        </div>
      </div>
    );
  }
  return (
    <div className="fade-in flex justify-center">
      <div className="border-destructive/40 text-destructive flex max-w-full items-start gap-1.5 rounded-lg border px-3 py-1 text-[12px]">
        <OctagonXIcon className="mt-0.5 size-3.5 shrink-0" />
        <span className="min-w-0 break-words whitespace-pre-wrap">Restart failed: {item.text}</span>
      </div>
    </div>
  );
}
