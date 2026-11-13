import { PencilIcon } from "lucide-react";
import { useCallback } from "react";

import { useThread, useThreadStore } from "~/app/threadStore";
import { canEditFrom, useRewind } from "~/lib/rewind";
import type { Item, ThreadState } from "~/protocol";

/**
 * "Edit from here" on a prompt that opened a turn: opens the confirmation to
 * cut the thread back to before it. Nothing at all where the thread cannot be
 * rewound (no RewindContext), and nothing on a prompt it cannot be rewound to
 * right now.
 */
export function RewindButton({ item, className }: { item: Item; className: string }) {
  const open = useRewind();
  if (!open) return null;
  return <Offered item={item} onOpen={open} className={className} />;
}

// Split out so only a thread that can rewind subscribes each prompt's button
// to the store. The pick is a boolean, so a streamed chunk rerenders nothing.
function Offered({
  item,
  onOpen,
  className,
}: {
  item: Item;
  onOpen: (item: Item) => void;
  className: string;
}) {
  const offered = useThread(
    useThreadStore(),
    useCallback((s: ThreadState | null) => canEditFrom(s, item), [item]),
  );
  if (!offered) return null;
  return (
    <button
      type="button"
      onClick={() => onOpen(item)}
      aria-label="Edit from here"
      className={className}
    >
      <PencilIcon className="size-3.5" />
      {/* One word, like its neighbours: the row has to fit beside copy and
          play at 360px. The confirmation says the rest. */}
      edit
    </button>
  );
}
