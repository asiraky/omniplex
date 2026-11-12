import { PinIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";
import { chipClass } from "./shared";

/** Whether the thread about to start lands in the sidebar's Pinned section. */
export function PinToggle({
  pinned,
  onToggle,
  className,
}: {
  pinned: boolean;
  onToggle: () => void;
  className?: string;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label="Pin thread"
      aria-pressed={pinned}
      title={pinned ? "Starts pinned" : "Starts unpinned"}
      onClick={onToggle}
      className={cn(chipClass, className, pinned && "text-foreground")}
    >
      <PinIcon className={cn(pinned && "fill-current")} />
    </Button>
  );
}
