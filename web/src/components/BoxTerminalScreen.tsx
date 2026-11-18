import {
  ArrowLeftIcon,
  EllipsisIcon,
  PanelLeftIcon,
  PowerIcon,
  ServerIcon,
  SquareTerminalIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { TerminalSurface } from "~/components/panel/TerminalSurface";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { endBoxShell, type TerminalTarget } from "~/lib/termSocket";
import { useIsDesktop } from "~/useMediaQuery";

// There is one box shell, so one id: every device attaches to the same one.
const BOX_TERM_ID = "box";
const BOX: TerminalTarget = { box: true };

/** `cwd` as the prompt would write it: under `home`, with `~` for it. */
export function homeRelative(cwd: string, home: string): string {
  if (cwd === home) return "~";
  const base = home.endsWith("/") ? home : `${home}/`;
  return cwd.startsWith(base) ? `~/${cwd.slice(base.length)}` : cwd;
}

/**
 * The shell on the box itself, in the main pane: beside the sidebar on a
 * desktop, the whole screen on a phone. The thread it replaced stays mounted
 * behind it, so closing is instant and lands where the user was.
 */
export function BoxTerminalScreen({
  sidebarOpen,
  onShowSidebar,
  onClose,
  onAddToChat,
}: {
  sidebarOpen: boolean;
  onShowSidebar: () => void;
  onClose: () => void;
  /** Output quoted for a thread's composer; absent when there is no thread to take it. */
  onAddToChat?: (quoted: string) => void;
}) {
  const isDesktop = useIsDesktop();
  const [where, setWhere] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [ending, setEnding] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);

  // Opened from a keyboard, the next thing typed is a command. Not on a phone,
  // where focus raises the keyboard over the output the user came to read.
  // xterm's input is its own hidden textarea, made by the time this runs.
  useEffect(() => {
    if (isDesktop) bodyRef.current?.querySelector<HTMLElement>("[data-terminal] textarea")?.focus();
    // Only as it opens: focus elsewhere later is the user's choice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const end = async () => {
    setEnding(true);
    await endBoxShell(BOX_TERM_ID);
    setEnding(false);
    setConfirming(false);
  };

  return (
    <section
      aria-label="Terminal"
      className="flex min-h-0 min-w-0 flex-1 flex-col pb-[env(safe-area-inset-bottom)]"
    >
      <header className="flex items-center gap-1 border-b px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:gap-2 md:px-3">
        {isDesktop ? (
          !sidebarOpen && (
            <IconButton label="Show threads" shortcut="toggleSidebar" onClick={onShowSidebar}>
              <PanelLeftIcon className="size-3.5" />
            </IconButton>
          )
        ) : (
          <IconButton label="Back" onClick={onClose}>
            <ArrowLeftIcon />
          </IconButton>
        )}
        {/* One line on a desktop; two on a phone, where the place would
            otherwise squeeze the name out. */}
        <div className="flex min-w-0 flex-1 flex-col md:flex-row md:items-center md:gap-2">
          <span className="flex items-center gap-2">
            <SquareTerminalIcon aria-hidden className="hidden size-4 shrink-0 md:block" />
            <h1 className="text-[13px] font-medium">Terminal</h1>
          </span>
          <span className="text-muted-foreground flex min-w-0 items-center gap-1.5 font-mono text-[11px]">
            <ServerIcon aria-hidden className="size-3 shrink-0" />
            <span className="truncate">
              {where ? `box · ${where}` : "box"}
            </span>
          </span>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton label="Terminal actions" className="text-muted-foreground hover:text-foreground">
              <EllipsisIcon />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            <DropdownMenuItem onSelect={() => setConfirming(true)} className="text-[13px]">
              <PowerIcon className="text-muted-foreground" />
              End shell…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {isDesktop && (
          <IconButton
            label="Close terminal"
            shortcut="toggleTerminal"
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground"
          >
            <XIcon />
          </IconButton>
        )}
      </header>

      <div ref={bodyRef} className="min-h-0 flex-1">
        <TerminalSurface
          termId={BOX_TERM_ID}
          target={BOX}
          onAddToChat={onAddToChat}
          onCwd={(cwd, root) => setWhere(homeRelative(cwd, root))}
        />
      </div>

      {/* Closing the screen only hangs up; this is the one way to stop the
          shell, and it stops it under every device watching it. */}
      <Dialog open={confirming} onOpenChange={(open) => !open && !ending && setConfirming(false)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>End the box shell?</DialogTitle>
            <DialogDescription>
              Whatever is running in it stops, on every device that has it open. The next open
              starts a fresh shell in your home directory.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirming(false)} disabled={ending}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void end()} disabled={ending}>
              End shell
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
