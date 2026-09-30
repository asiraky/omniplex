import { CheckIcon, CopyIcon } from "lucide-react";

import { useCopy } from "~/lib/clipboard";
import type { Item } from "~/protocol";

function receivedTime(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/**
 * The footer under a message: when it arrived, and a one-click copy of
 * the raw text. Quiet by design — metadata should not compete with the prose —
 * so it fades in on hover on a desktop and stays small everywhere.
 */
export function MessageMeta({ item }: { item: Item }) {
  const { copied, copy } = useCopy();
  const time = receivedTime(item.receivedAt);
  if (!time && !item.text) return null;

  return (
    <div className="text-muted-foreground flex items-center gap-1.5 text-[13px] opacity-100 transition-opacity md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100">
      {time && <span className="font-mono">{time}</span>}
      <button
        type="button"
        onClick={() => void copy(item.text ?? "")}
        aria-label="Copy message"
        className="hover:text-foreground focus-visible:ring-ring flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 outline-none focus-visible:ring-2"
      >
        {copied ? (
          <CheckIcon className="text-success size-3.5" />
        ) : (
          <CopyIcon className="size-3.5" />
        )}
        {copied ? "copied" : "copy"}
      </button>
    </div>
  );
}
