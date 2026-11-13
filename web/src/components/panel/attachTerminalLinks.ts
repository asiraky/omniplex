import type { IDisposable, Terminal } from "@xterm/xterm";

import { resolvePath } from "~/lib/paths";
import { findTerminalLinks, type TerminalLink } from "~/lib/terminalLinks";
import { cellAtPoint, linksAtRow, rangeContains, type BufferLink } from "~/lib/xtermLinks";

// A touch that stays this close and lifts this soon is a tap, not a scroll
// or a long press.
const TAP_SLOP_PX = 10;
const TAP_MS = 500;
// How long after a tap opened a link xterm's own activation of it is ignored,
// should the browser deliver the tap's mouse events anyway.
const TAP_GRACE_MS = 800;

export interface TerminalLinkOptions {
  /**
   * Where a clicked path goes, read at each use so the caller's callback can
   * change. Without one only URLs are links.
   */
  openPath: () => ((path: string, line?: number) => void) | undefined;
  /** The shell's working directory, absolute, or null when it is not known. */
  cwd: () => Promise<string | null>;
}

function openUrl(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * Makes the URLs and file paths in a terminal's output clickable, and
 * tappable. `host` is the element the terminal was opened in. A relative path
 * is made absolute against the shell's working directory before it is handed
 * on, since the shell may have moved since the terminal started.
 */
export function attachTerminalLinks(term: Terminal, host: HTMLElement, opts: TerminalLinkOptions): IDisposable {
  let disposed = false;
  // When a tap last opened a link itself (see the pointer handlers below).
  let tappedAt = -Infinity;
  const tappedRecently = () => performance.now() - tappedAt < TAP_GRACE_MS;

  // Paths are links only when there is somewhere to open them, read at the
  // hover so the callback can change.
  const linksAt = (y: number): BufferLink[] => {
    const links = linksAtRow(term.buffer.active, y, findTerminalLinks);
    return opts.openPath() ? links : links.filter((l) => l.link.kind === "url");
  };
  const openPath = async (path: string, line?: number) => {
    let resolved = path;
    if (!path.startsWith("/") && !path.startsWith("~")) {
      const dir = await opts.cwd();
      if (disposed) return;
      if (dir) resolved = resolvePath(path, dir);
    }
    opts.openPath()?.(resolved, line);
  };
  const follow = (link: TerminalLink) => {
    if (link.kind === "url") openUrl(link.url);
    else void openPath(link.path, link.line);
  };

  const provider = term.registerLinkProvider({
    provideLinks: (y, callback) => {
      const links = linksAt(y);
      // Undefined, not empty, for none: xterm reads an empty answer from a
      // provider as "has links" and lets it shadow the ones after it.
      callback(
        links.length
          ? links.map(({ link, range, text }) => ({
              range,
              text,
              // xterm activates on any mouseup over the link, the end of a
              // drag or a double-click that selected it included; a gesture
              // that left a selection was after the text, not the link.
              activate: () => {
                if (!tappedRecently() && !term.hasSelection()) follow(link);
              },
            }))
          : undefined,
      );
    },
  });

  // A tap opens a link without help from xterm's linkifier. That one works
  // off hover, and a tap's hover is only the mousemove the browser makes up
  // just before its mousedown; WebKit is documented to drop the rest of a
  // tap's mouse events when that mousemove changes the page, which the
  // hover underline does. So a touch is matched against the links here, on
  // the pointerup (a user gesture on a phone, so the new tab is allowed),
  // and its touchend is cancelled so no mouse events follow: no second open
  // through xterm, and no focus on xterm's input, which would raise the
  // keyboard over whatever the link just opened. An app that reads the
  // mouse (vim, htop) gets the tap instead, as it would a click.
  let touch: { id: number; x: number; y: number; t: number } | null = null;
  let swallowTouchEnd = false;
  const onPointerDown = (e: PointerEvent) => {
    touch =
      e.pointerType === "touch" && e.isPrimary ? { id: e.pointerId, x: e.clientX, y: e.clientY, t: e.timeStamp } : null;
  };
  const onPointerCancel = () => {
    touch = null;
  };
  const onPointerUp = (e: PointerEvent) => {
    const start = touch;
    touch = null;
    if (!start || e.pointerId !== start.id) return;
    if (Math.hypot(e.clientX - start.x, e.clientY - start.y) > TAP_SLOP_PX) return;
    if (e.timeStamp - start.t > TAP_MS || term.modes.mouseTrackingMode !== "none") return;
    const screen = term.element?.querySelector(".xterm-screen");
    if (!screen) return;
    const cell = cellAtPoint(
      screen.getBoundingClientRect(),
      { cols: term.cols, rows: term.rows, viewportY: term.buffer.active.viewportY },
      e.clientX,
      e.clientY,
    );
    const hit = cell && linksAt(cell.y).find((l) => rangeContains(l.range, cell));
    if (!hit) return;
    tappedAt = performance.now();
    swallowTouchEnd = true;
    follow(hit.link);
  };
  // A contact's pointerup comes before its touchend.
  const onTouchEnd = (e: TouchEvent) => {
    if (swallowTouchEnd && e.cancelable) e.preventDefault();
    swallowTouchEnd = false;
  };
  host.addEventListener("pointerdown", onPointerDown, true);
  host.addEventListener("pointerup", onPointerUp, true);
  host.addEventListener("pointercancel", onPointerCancel, true);
  host.addEventListener("touchend", onTouchEnd, { capture: true, passive: false });

  return {
    dispose() {
      disposed = true;
      host.removeEventListener("pointerdown", onPointerDown, true);
      host.removeEventListener("pointerup", onPointerUp, true);
      host.removeEventListener("pointercancel", onPointerCancel, true);
      host.removeEventListener("touchend", onTouchEnd, true);
      provider.dispose();
    },
  };
}
