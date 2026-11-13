import { ShieldIcon } from "lucide-react";

import { cn } from "~/lib/utils";
import type { Approval, Item } from "~/protocol";

import { usePromptFocus } from "./promptFocus";

const LABEL: Record<Approval, string> = {
  pending: "Needs approval",
  allowed: "Allowed",
  allowed_always: "Always allowed",
  denied: "Denied",
  cancelled: "Cancelled",
};

const CHIP = "shrink-0 rounded-full border px-1.5 py-px font-mono text-[10px] leading-tight whitespace-nowrap";

/**
 * How the permission a tool call asked for went, beside its status. Pending,
 * it is the way to the prompt above the composer, which is where it is
 * answered: tapping it brings that prompt into view. Never wraps; whatever
 * sits beside it truncates instead.
 */
export function ApprovalChip({ approval }: { approval: Approval }) {
  const focusPrompt = usePromptFocus();
  if (approval !== "pending") {
    return <span className={cn(CHIP, "text-muted-foreground")}>{LABEL[approval] ?? approval}</span>;
  }
  const tone = "border-attention/40 bg-attention-surface text-attention-foreground";
  if (!focusPrompt) return <span className={cn(CHIP, tone)}>{LABEL.pending}</span>;
  return (
    <button
      type="button"
      onClick={() => focusPrompt("permission")}
      className={cn(
        CHIP,
        tone,
        "hit-44 focus-visible:ring-ring relative cursor-pointer outline-none focus-visible:ring-2",
      )}
    >
      {LABEL.pending}
    </button>
  );
}

/**
 * A permission asked for with no tool call here to carry it, as one line in
 * its place: what was asked, and how it went.
 */
export function ApprovalRecord({ item }: { item: Item }) {
  return (
    <div className="fade-in text-muted-foreground flex min-h-11 items-center gap-2 px-1.5 py-1.5 md:min-h-0">
      <ShieldIcon aria-hidden className="size-3.5 shrink-0" />
      <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{item.title || "Permission"}</span>
      {item.approval && <ApprovalChip approval={item.approval} />}
    </div>
  );
}
