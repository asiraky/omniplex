import { FolderGit2Icon, FolderOpenIcon } from "lucide-react";
import { useState } from "react";

import { FolderBrowser, GitHubPicker } from "~/components/FolderSources";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import { cn } from "~/lib/utils";
import type { GitHubRepo, Project } from "~/protocol";

/** Where a new project's first folder comes from. At most one is set. */
export interface NewProjectRequest {
  name?: string;
  path?: string;
  url?: string;
}

/**
 * New project. A name is enough: Omniplex makes the folder. The two shortcuts
 * start from something that already exists, a repo on GitHub or a folder on
 * the machine running Omniplex.
 */
export function NewProject({
  onCreate,
  listRepos,
  onClose,
}: {
  onCreate: (req: NewProjectRequest) => Promise<Project>;
  listRepos: () => Promise<GitHubRepo[]>;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [from, setFrom] = useState<"github" | "folder" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const create = async (req: NewProjectRequest) => {
    setBusy(true);
    setError(null);
    try {
      await onCreate({ ...req, name: name.trim() || undefined });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const shortcut = (id: "github" | "folder", label: string, Icon: typeof FolderGit2Icon) => (
    <Button
      variant="outline"
      aria-pressed={from === id}
      onClick={() => setFrom(from === id ? null : id)}
      className={cn("flex-1", from === id && "bg-accent")}
    >
      <Icon />
      {label}
    </Button>
  );

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent
        fullscreenOnMobile
        className="flex max-h-[min(90dvh,44rem)] flex-col gap-0 p-0 md:max-w-lg"
      >
        <DialogHeader className="border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>New project</DialogTitle>
          <DialogDescription>A place for threads about one piece of work.</DialogDescription>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-5">
          <form
            className="space-y-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              if (name.trim() && !from) void create({});
            }}
          >
            <Label htmlFor="new-project-name">Name</Label>
            <div className="flex gap-2">
              <Input
                id="new-project-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={from ? "Optional" : "Bowerbird"}
                autoFocus
                className="min-w-0 flex-1"
              />
              {!from && (
                <Button type="submit" disabled={!name.trim() || busy}>
                  {busy && <Spinner aria-hidden className="size-4" />}
                  Create
                </Button>
              )}
            </div>
            {!from && (
              <p className="text-muted-foreground text-[11px]">
                Makes a folder for it in your projects folder.
              </p>
            )}
          </form>

          <div className="space-y-2">
            <p className="text-muted-foreground text-[11px]">Or start from</p>
            <div className="flex gap-2">
              {shortcut("github", "GitHub", FolderGit2Icon)}
              {shortcut("folder", "A folder here", FolderOpenIcon)}
            </div>
          </div>

          {from === "github" && (
            <GitHubPicker listRepos={listRepos} busy={busy} onChoose={(url) => void create({ url })} />
          )}
          {from === "folder" && (
            <FolderBrowser busy={busy} onChoose={(path) => void create({ path })} />
          )}

          {error && (
            <Alert variant="destructive">
              <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
            </Alert>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
