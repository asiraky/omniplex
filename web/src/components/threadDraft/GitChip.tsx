import { GitBranchIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "~/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Spinner } from "~/components/ui/spinner";
import { chunk } from "~/lib/chunk";
import type { Folder, UserConfig } from "~/protocol";
import { useIdlePrefetch } from "~/useIdlePrefetch";
import { Chip } from "./parts";
import type { WorkspaceChoiceState } from "./useWorkspaceChoice";

// The popover's body, and the branch combobox and Base dropdown in it, stay
// out of the first load. The chip fetches them once the page is idle, so
// opening it rarely waits; when it does, a spinner holds the popover's place.
const choice = chunk(() => import("./GitChoice").then((m) => m.GitChoice));

interface Props {
  folder: Folder;
  git: WorkspaceChoiceState;
  userConfig: UserConfig | null;
}

/**
 * The popover's body once its code is in. It mounts with each opening, so a
 * fetch that failed is tried again the next time the chip is opened.
 */
function LazyGitChoice(props: Props) {
  const Loaded = choice.get();
  const [failed, setFailed] = useState(false);
  const [, setArrived] = useState(0);
  useEffect(() => {
    if (Loaded) return;
    let live = true;
    void choice.load().then((c) => {
      if (!live) return;
      if (c) setArrived((n) => n + 1);
      else setFailed(true);
    });
    return () => {
      live = false;
    };
  }, [Loaded]);

  if (Loaded) return <Loaded {...props} />;
  return (
    <div className="text-muted-foreground flex min-h-24 flex-col items-center justify-center gap-3 p-3 text-center text-[12px]">
      {failed ? (
        <>
          <p>Couldn't load. Reloading the page tries again, but drops anything unsent.</p>
          <Button variant="outline" size="sm" onClick={() => location.reload()}>
            Reload
          </Button>
        </>
      ) : (
        <Spinner />
      )}
    </div>
  );
}

/** Which checkout of a git repo the thread works in. */
export function GitChip({ folder, git, userConfig }: Props) {
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
