import { useEffect, useState, type ComponentType } from "react";

import { chunk, type Chunk } from "~/lib/chunk";
import { useIsDesktop } from "~/useMediaQuery";

import type { CommandPaletteProps } from "./CommandPalette";
import { Button } from "./ui/button";
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
  return (
    <OnDemand
      chunk={palette}
      props={props}
      title="Loading the command palette"
      look={isDesktop ? "palette" : "sheet"}
    />
  );
}

export function LazyShortcutsHelp(props: OverlayProps) {
  return <OnDemand chunk={help} props={props} title="Loading keyboard shortcuts" look="help" />;
}

interface OverlayProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * An overlay whose code arrives on first open. It mounts then and stays
 * mounted, so it can animate closed. Until the code is in, a placeholder
 * dialog takes focus and Escape, so keys meant for the overlay do not land in
 * the composer under it. If the fetch fails it says so. The next opening tries
 * again, but Chrome keeps a failed import for the life of the page, so the way
 * out it offers is a reload.
 */
export function OnDemand<P extends OverlayProps>({
  chunk,
  props,
  title,
  look,
}: {
  chunk: Chunk<ComponentType<P>>;
  props: P;
  title: string;
  /** The placeholder takes the shape of the overlay it stands in for. */
  look: "palette" | "sheet" | "help";
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
      if (open && chunk.get()) e.preventDefault();
    },
  };
  const body = (
    <div className="text-muted-foreground flex min-h-24 flex-1 flex-col items-center justify-center gap-3 p-6 text-center text-sm">
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
  if (look === "sheet") {
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
      <DialogContent
        {...contentProps}
        // Only a failure stays up long enough to want a way out other than Escape.
        showCloseButton={failed}
        fullscreenOnMobile={look === "help"}
        className={look === "palette" ? "top-[15%] translate-y-0 p-0 sm:max-w-xl" : "p-0 md:max-w-lg"}
      >
        <DialogTitle className="sr-only">{title}</DialogTitle>
        {body}
      </DialogContent>
    </Dialog>
  );
}
