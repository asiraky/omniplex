import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { MessageSquarePlusIcon, TextQuoteIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { findTerminalLinks, type TerminalLink } from "~/lib/terminalLinks";
import { lastTerminalLines, quoteTerminalOutput } from "~/lib/terminalQuote";
import { openTermSocket, type TermEnd, type TermSocket, type TermStatus, type TerminalTarget } from "~/lib/termSocket";
import { toast } from "~/lib/toast";
import { cn } from "~/lib/utils";
import { bufferRows, cellAtPoint, linksAtRow, rangeContains, type BufferLink } from "~/lib/xtermLinks";
import { useLatest } from "~/useLatest";
import { useCanHover } from "~/useMediaQuery";

const SHELL_ENDED: Record<TermEnd, string> = {
  exited: "The shell exited.",
  gone: "This shell is no longer running: it was closed, sat idle too long, or the server restarted.",
  failed: "The shell could not start.",
};

const LOGIN_ENDED: Record<TermEnd, string> = {
  exited: "The sign-in has ended.",
  gone: "The sign-in is no longer running.",
  failed: "The sign-in could not start.",
};

/** The choices of "send the last N lines": a command's tail, a screenful, a long log. */
const LAST_LINES = [20, 50, 200];

// A touch that stays this close and lifts this soon is a tap, not a scroll
// or a long press.
const TAP_SLOP_PX = 10;
const TAP_MS = 500;
// How long after a tap opened a link xterm's own activation of it is ignored,
// should the browser deliver the tap's mouse events anyway.
const TAP_GRACE_MS = 800;

function openUrl(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * One terminal tab: an xterm bound to a pty the server spawned in the
 * thread's checkout, under a terminal id the caller holds on to. The shell
 * outlives this component and its socket: unmounting only hangs up, and a
 * dropped connection reconnects and replays what was missed. The shell's
 * lifetime is the tab's — it ends when the tab is closed (the caller ends it
 * by id), when the process exits, after 30 minutes with nobody attached, or
 * when the server restarts.
 */
export function TerminalSurface({
  termId,
  target,
  onEnded,
  onAddToChat,
  onOpenPath,
}: {
  /**
   * Which shell. Must be stable across remounts: StrictMode's double mount and
   * a page reload both reattach to the shell by this id.
   */
  termId: string;
  target: TerminalTarget;
  /** The process ended, the server no longer has it, or it could not start. */
  onEnded?: () => void;
  /**
   * Output quoted for the composer, already fenced. Given, the terminal offers
   * "Add to chat" on a selection and "send the last lines".
   */
  onAddToChat?: (quoted: string) => void;
  /** A file path clicked in the output. Without it only URLs are links. */
  onOpenPath?: (path: string, line?: number) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const socketRef = useRef<TermSocket | null>(null);
  const termRef = useRef<Terminal | null>(null);
  // Whether the last-lines menu closed on a send, for where focus goes next.
  const sentRef = useRef(false);
  const [status, setStatus] = useState<TermStatus>("connecting");
  const [ended, setEnded] = useState<TermEnd | null>(null);
  const [selected, setSelected] = useState(false);
  const onEndedRef = useLatest(onEnded);
  const onOpenPathRef = useLatest(onOpenPath);
  const canHover = useCanHover();
  const targetKey = "thread" in target ? `thread:${target.thread}` : `login:${target.login}`;
  const login = "login" in target;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setEnded(null);

    // When a tap last opened a link itself (see the pointer handlers below).
    let tappedAt = -Infinity;
    const tappedRecently = () => performance.now() - tappedAt < TAP_GRACE_MS;

    const term = new Terminal({
      fontSize: 12,
      // xterm takes a literal stack rather than the CSS variable, so this has
      // to repeat --font-mono's Windows entries: ui-monospace is Apple-only,
      // and the generic fallback lands on Courier New there.
      fontFamily:
        'ui-monospace, SFMono-Regular, Menlo, "Cascadia Mono", Consolas, "Liberation Mono", monospace',
      cursorBlink: true,
      convertEol: false,
      theme: { background: "#00000000" },
      allowTransparency: true,
      // OSC 8 hyperlinks, the ones a program marks up itself (ls --hyperlink,
      // gh, cargo). xterm's default asks confirm() first; a link in our own
      // shell's output is no more dangerous than one we found by pattern.
      // Only http(s) reach here: xterm drops other schemes unless told not to.
      linkHandler: {
        activate: (_event, uri) => {
          if (!tappedRecently() && /^https?:\/\//i.test(uri)) openUrl(uri);
        },
      },
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    // URLs and paths found in the text. Paths are links only when there is
    // somewhere to open them, read at the hover so the callback can change.
    const linksAt = (y: number): BufferLink[] => {
      const links = linksAtRow(term.buffer.active, y, findTerminalLinks);
      return onOpenPathRef.current ? links : links.filter((l) => l.link.kind === "url");
    };
    const follow = (link: TerminalLink) => {
      if (link.kind === "url") openUrl(link.url);
      else onOpenPathRef.current?.(link.path, link.line);
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
                activate: () => {
                  if (!tappedRecently()) follow(link);
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
        e.pointerType === "touch" && e.isPrimary
          ? { id: e.pointerId, x: e.clientX, y: e.clientY, t: e.timeStamp }
          : null;
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

    const selection = term.onSelectionChange(() => setSelected(term.hasSelection()));

    const socket = openTermSocket(termId, target, {
      onData: (bytes) => term.write(bytes),
      onReset: () => term.reset(),
      onStatus: setStatus,
      onEnded: (reason) => {
        setEnded(reason);
        onEndedRef.current?.();
      },
    });
    socketRef.current = socket;
    socket.resize(term.cols, term.rows);

    const data = term.onData((d) => socket.send(d));
    const resize = term.onResize(({ cols, rows }) => socket.resize(cols, rows));

    // Refit when the panel is resized — the panel drag changes our box without
    // any window resize firing.
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(host);

    return () => {
      ro.disconnect();
      host.removeEventListener("pointerdown", onPointerDown, true);
      host.removeEventListener("pointerup", onPointerUp, true);
      host.removeEventListener("pointercancel", onPointerCancel, true);
      host.removeEventListener("touchend", onTouchEnd, true);
      provider.dispose();
      selection.dispose();
      data.dispose();
      resize.dispose();
      socket.dispose();
      socketRef.current = null;
      termRef.current = null;
      setSelected(false);
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- a target is an identity, not a fresh object per render
  }, [termId, targetKey]);

  const restart = () => {
    setEnded(null);
    socketRef.current?.restart();
  };

  const addSelection = () => {
    const term = termRef.current;
    if (!term) return;
    const quoted = quoteTerminalOutput(term.getSelection());
    term.clearSelection();
    if (quoted) onAddToChat?.(quoted);
  };

  const sendLast = (n: number) => {
    const term = termRef.current;
    if (!term) return;
    const quoted = quoteTerminalOutput(lastTerminalLines(bufferRows(term.buffer.active), n));
    if (!quoted) {
      toast.info("Nothing in the terminal to send");
      return;
    }
    sentRef.current = true;
    onAddToChat?.(quoted);
  };

  const pill =
    "flex items-center gap-1.5 rounded-full bg-black/75 px-2 py-0.5 text-[11px] text-white/80 ring-1 ring-white/15";
  const pillButton = cn(
    pill,
    "pointer-events-auto h-6 transition-colors outline-none hover:bg-black/90 hover:text-white focus-visible:ring-white/60 pointer-coarse:h-9 pointer-coarse:px-3 pointer-coarse:text-[12px]",
  );

  return (
    <div className="group/terminal relative h-full min-h-0 bg-black/90 p-1.5">
      <div ref={hostRef} className="h-full min-h-0 [&_.xterm]:h-full" />
      {/* One row in the top corner, so the pills sit side by side rather
          than on top of each other. Only the buttons take pointer events; the
          rest lets clicks and drags through to the terminal. */}
      <div className="pointer-events-none absolute top-2 right-2 flex items-center gap-1.5">
        {/* The output stays up while the socket is away: the shell is still
            running, and what it printed meanwhile replays on reconnect. */}
        {status === "reconnecting" && !ended && (
          <div role="status" className={cn(pill, "h-6 pointer-coarse:h-9")}>
            <span className="size-1.5 animate-pulse rounded-full bg-amber-400" />
            Reconnecting…
          </div>
        )}
        {onAddToChat && selected && !ended && (
          <button
            type="button"
            // The terminal keeps its selection and its focus through the click.
            onMouseDown={(e) => e.preventDefault()}
            onClick={addSelection}
            className={pillButton}
          >
            <MessageSquarePlusIcon className="size-3.5" />
            Add to chat
          </button>
        )}
        {/* Selecting on a touch screen is hard going in xterm, so this is the
            phone's way to quote output; with a mouse it waits for the pointer
            to come over the terminal rather than sit on top of the output. */}
        {onAddToChat && !ended && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Send recent output to chat"
                onMouseDown={(e) => e.preventDefault()}
                className={cn(
                  pillButton,
                  canHover &&
                    "opacity-0 group-hover/terminal:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
                )}
              >
                <TextQuoteIcon className="size-3.5" />
                <span className="hidden pointer-coarse:inline">Send output</span>
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="end"
              // Not back to the trigger: after a send the caller puts focus
              // where the quote went, and dismissed with a mouse it goes back
              // to the shell. A touch screen gets neither, since focusing the
              // terminal raises the keyboard.
              onCloseAutoFocus={(e) => {
                e.preventDefault();
                if (canHover && !sentRef.current) termRef.current?.focus();
                sentRef.current = false;
              }}
            >
              <DropdownMenuLabel>Send to chat</DropdownMenuLabel>
              {LAST_LINES.map((n) => (
                <DropdownMenuItem key={n} onSelect={() => sendLast(n)}>
                  Last {n} lines
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {/* A finished sign-in leaves its last words on screen — "logged in as…",
          or the error — so the notice sits below rather than over them. */}
      {ended && (
        <div
          className={cn(
            "absolute flex items-center justify-center gap-3 bg-black/70 text-center",
            login ? "inset-x-0 bottom-0 flex-row px-3 py-2" : "inset-0 flex-col px-4",
          )}
        >
          <p className="text-[13px] text-white/80">{(login ? LOGIN_ENDED : SHELL_ENDED)[ended]}</p>
          <button
            type="button"
            onClick={restart}
            className="rounded-md border border-white/30 px-3 py-1 text-[12px] text-white/90 transition-colors hover:bg-white/10"
          >
            {login ? "Start again" : "Start a new shell"}
          </button>
        </div>
      )}
    </div>
  );
}
