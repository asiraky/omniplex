import { useEffect, useState, type ComponentType } from "react";

import { chunk, type Chunk } from "~/lib/chunk";
import { useIsDesktop } from "~/useMediaQuery";

import type { CommandPaletteProps } from "./CommandPalette";
import { Dialog, DialogContent, DialogTitle } from "./ui/dialog";
import { Sheet, SheetContent, SheetTitle } from "./ui/sheet";
import { Spinner } from "./ui/spinner";

// The palette and the shortcuts list stay out of the first load. Both are
// fetched once the page is idle, so the first ⌘K rarely waits; when it does,
// a placeholder holds the keys until the real one arrives.
const palette = chunk(() => import("./CommandPalette").then((m) => m.CommandPalette));
const help = chunk(() => import("./ShortcutsHelp").then((m) => m.ShortcutsHelp));

/** Fetches the palette and the shortcuts list once the page has settled. */
export function usePrefetchOverlays() {
  useEffect(() => {
    const prefetch = () => {
      void palette.load();
      void help.load();
    };
    if (typeof requestIdleCallback === "function") {
      const id = requestIdleCallback(prefetch, { timeout: 5000 });
      return () => cancelIdleCallback(id);
    }
    const id = setTimeout(prefetch, 2000);
    return () => clearTimeout(id);
  }, []);
}

export function LazyCommandPalette(props: CommandPaletteProps) {
  const isDesktop = useIsDesktop();
  return <OnDemand chunk={palette} props={props} title="Loading the command palette" sheet={!isDesktop} />;
}

export function LazyShortcutsHelp(props: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return <OnDemand chunk={help} props={props} title="Loading keyboard shortcuts" />;
}

interface OverlayProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * An overlay whose code arrives on first open. It mounts then and stays
 * mounted, so it can animate closed. Until the code is in, a placeholder
 * dialog takes focus and Escape, so keys meant for the overlay do not land in
 * the composer under it; if the fetch fails it says so, and the next opening
 * tries again.
 */
export function OnDemand<P extends OverlayProps>({
  chunk,
  props,
  title,
  sheet = false,
}: {
  chunk: Chunk<ComponentType<P>>;
  props: P;
  title: string;
  /** The placeholder is a full-height sheet, like the palette on a phone. */
  sheet?: boolean;
}) {
  const { open, onOpenChange } = props;
  const [seenOpen, setSeenOpen] = useState(open);
  const [opened, setOpened] = useState(open);
  const [failed, setFailed] = useState(false);
  const [, setArrived] = useState(0);
  if (seenOpen !== open) {
    setSeenOpen(open);
    if (open) {
      setOpened(true);
      setFailed(false);
    }
  }

  const Loaded = chunk.get();
  useEffect(() => {
    if (!open || Loaded) return;
    let live = true;
    void chunk.load().then((c) => {
      if (!live) return;
      if (c) setArrived((n) => n + 1);
      else setFailed(true);
    });
    return () => {
      live = false;
    };
  }, [open, Loaded, chunk]);

  if (!opened) return null;
  if (Loaded) return <Loaded {...props} />;

  const contentProps = {
    "aria-describedby": undefined,
    // Replaced by the overlay once it arrives, which focuses its own field:
    // handing focus back to whatever had it would take it away again.
    onCloseAutoFocus: (e: Event) => {
      if (chunk.get()) e.preventDefault();
    },
  };
  const body = (
    <div className="text-muted-foreground flex min-h-24 items-center justify-center p-6 text-center text-sm">
      {failed ? "Couldn't load. Check the connection and open it again." : <Spinner />}
    </div>
  );
  if (sheet) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          {...contentProps}
          className="h-[100dvh] gap-0 p-0 pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)]"
        >
          <SheetTitle className="sr-only">{title}</SheetTitle>
          {body}
        </SheetContent>
      </Sheet>
    );
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent {...contentProps} className="top-[15%] translate-y-0 p-0 sm:max-w-xl">
        <DialogTitle className="sr-only">{title}</DialogTitle>
        {body}
      </DialogContent>
    </Dialog>
  );
}
