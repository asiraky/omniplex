import { GitBranchIcon } from "lucide-react";
import { lazy, Suspense, useEffect } from "react";

import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Spinner } from "~/components/ui/spinner";
import type { Folder, UserConfig } from "~/protocol";
import { Chip } from "./parts";
import type { WorkspaceChoiceState } from "./useWorkspaceChoice";

// The popover's body, and the branch combobox in it, stay out of the first
// load. The chip fetches them once the page is idle, so opening it rarely
// waits; when it does, a spinner holds the popover's place.
const loadChoice = () => import("./GitChoice");
const GitChoice = lazy(loadChoice);

function usePrefetchChoice() {
  useEffect(() => {
    const prefetch = () => void loadChoice().catch(() => {});
    if (typeof requestIdleCallback === "function") {
      const id = requestIdleCallback(prefetch, { timeout: 5000 });
      return () => cancelIdleCallback(id);
    }
    const id = setTimeout(prefetch, 2000);
    return () => clearTimeout(id);
  }, []);
}

/** Which checkout of a git repo the thread works in. */
export function GitChip({
  folder,
  git,
  userConfig,
}: {
  folder: Folder;
  git: WorkspaceChoiceState;
  userConfig: UserConfig | null;
}) {
  usePrefetchChoice();
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Chip label="Git" icon={<GitBranchIcon />}>
          {git.label}
        </Chip>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(22rem,calc(100vw-2rem))] space-y-2">
        <Suspense fallback={<Spinner className="text-muted-foreground mx-auto my-6" />}>
          <GitChoice folder={folder} git={git} userConfig={userConfig} />
        </Suspense>
      </PopoverContent>
    </Popover>
  );
}
