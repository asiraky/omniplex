import { GitBranchIcon } from "lucide-react";

import { ChunkFallback } from "~/components/ChunkFallback";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { chunk } from "~/lib/chunk";
import { useChunk, useIdlePrefetch } from "~/useChunk";
import type { GitChoiceProps } from "./GitChoice";
import { Chip } from "./parts";

// The popover's body, and the branch combobox and Base dropdown in it, stay
// out of the first load. The chip fetches them once the page is idle, so
// opening it rarely waits; when it does, a spinner holds the popover's place.
const choice = chunk(() => import("./GitChoice").then((m) => m.GitChoice));

/**
 * The popover's body once its code is in. It mounts with each opening, so a
 * fetch that failed is asked for again then; whether that refetches is the
 * browser's call, which is why the failure offers a reload.
 */
function LazyGitChoice(props: GitChoiceProps) {
  const { loaded: Loaded, failed } = useChunk(choice);
  if (Loaded) return <Loaded {...props} />;
  return <ChunkFallback failed={failed} className="p-3 text-[12px]" />;
}

/** Which checkout of a git repo the thread works in. */
export function GitChip({ folder, git, userConfig }: GitChoiceProps) {
  useIdlePrefetch(choice);
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Chip label="Git" icon={<GitBranchIcon />}>
          {git.label}
        </Chip>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(22rem,calc(100vw-2rem))] space-y-2">
        <LazyGitChoice folder={folder} git={git} userConfig={userConfig} />
      </PopoverContent>
    </Popover>
  );
}
