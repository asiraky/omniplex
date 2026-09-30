import { TerminalIcon } from "lucide-react";

import { RecentSkills } from "~/components/RecentSkills";
import type { ComposerItem } from "~/protocol";

export function EmptyTranscript({
  recents,
  recentsSeeded,
  onPickRecent,
}: {
  recents: ComposerItem[];
  recentsSeeded: boolean;
  onPickRecent?: (item: ComposerItem) => void;
}) {
  return (
    // Centred in whatever the composer leaves, rather than a fixed
    // drop from the top, so it scrolls only when it truly can't fit.
    <div className="text-muted-foreground my-auto flex flex-col items-center gap-2 py-10 text-center">
      <TerminalIcon className="size-5 opacity-60" />
      <p className="text-sm">Nothing yet.</p>
      <p className="text-[13px]">Send a prompt to start the turn.</p>
      {recents.length > 0 && onPickRecent && (
        <div className="mt-6 w-full">
          <RecentSkills items={recents} seeded={recentsSeeded} onPick={onPickRecent} />
        </div>
      )}
    </div>
  );
}
