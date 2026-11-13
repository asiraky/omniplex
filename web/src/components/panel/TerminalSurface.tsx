import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";

import type { OpenPath } from "~/lib/openPath";
import { bufferRows, lastTerminalLines, quoteTerminalOutput } from "~/lib/terminalQuote";
import { openTermSocket, type TermEnd, type TermSocket, type TermStatus, type TerminalTarget } from "~/lib/termSocket";
import { toast } from "~/lib/toast";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { attachTerminalLinks } from "./attachTerminalLinks";
import { TerminalActions } from "./TerminalActions";

// Lines xterm keeps above the screen. The server is asked for only this many
// plus the visible rows on a fresh attach, so the two must be the same number.
const SCROLLBACK = 1000;

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
  /**
   * A file path clicked in the output: relative to the shell's start
   * directory when under it, else absolute, or as printed when the shell
   * cannot say where it is. Without it only URLs are links.
   */
  onOpenPath?: OpenPath;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const socketRef = useRef<TermSocket | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const [status, setStatus] = useState<TermStatus>("connecting");
  const [ended, setEnded] = useState<TermEnd | null>(null);
  const [selected, setSelected] = useState(false);
  const onEndedRef = useLatest(onEnded);
  const onOpenPathRef = useLatest(onOpenPath);
  const targetKey = "thread" in target ? `thread:${target.thread}` : `login:${target.login}`;
  const login = "login" in target;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setEnded(null);

    const term = new Terminal({
      fontSize: 12,
      // xterm takes a literal stack rather than the CSS variable, so this has
      // to repeat --font-mono's Windows entries: ui-monospace is Apple-only,
      // and the generic fallback lands on Courier New there.
      fontFamily:
        'ui-monospace, SFMono-Regular, Menlo, "Cascadia Mono", Consolas, "Liberation Mono", monospace',
      cursorBlink: true,
      scrollback: SCROLLBACK,
      convertEol: false,
      theme: { background: "#00000000" },
      allowTransparency: true,
      // OSC 8 hyperlinks, the ones a program marks up itself (gh, cargo,
      // compilers), keep xterm's default handler, which shows the real target
      // in a confirm() before opening it: the text printed for one need not be
      // where it goes.
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    const selection = term.onSelectionChange(() => setSelected(term.hasSelection()));

    const socket = openTermSocket(termId, target, {
      onData: (bytes) => term.write(bytes),
      onReset: () => term.reset(),
      onStatus: setStatus,
      onEnded: (reason) => {
        setEnded(reason);
        onEndedRef.current?.();
      },
    }, () => SCROLLBACK + term.rows);
    socketRef.current = socket;
    socket.resize(term.cols, term.rows);

    const links = attachTerminalLinks(term, host, {
      openPath: () => onOpenPathRef.current,
      where: () => socket.where(),
    });

    const data = term.onData((d) => socket.send(d));
    const resize = term.onResize(({ cols, rows }) => socket.resize(cols, rows));

    // Refit when the panel is resized — the panel drag changes our box without
    // any window resize firing.
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(host);

    return () => {
      ro.disconnect();
      links.dispose();
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
    if (!quoted) {
      toast.info("Nothing in the selection to add");
      return;
    }
    term.clearSelection();
    onAddToChat?.(quoted);
  };

  const sendLast = (n: number) => {
    const term = termRef.current;
    if (!term) return false;
    const quoted = quoteTerminalOutput(lastTerminalLines(bufferRows(term.buffer.active), n));
    if (!quoted) {
      toast.info("Nothing in the terminal to send");
      return false;
    }
    onAddToChat?.(quoted);
    return true;
  };

  return (
    <div className="group/terminal relative h-full min-h-0 bg-black/90 p-1.5">
      <div ref={hostRef} className="h-full min-h-0 [&_.xterm]:h-full" />
      <TerminalActions
        reconnecting={status === "reconnecting" && !ended}
        selected={selected}
        quote={onAddToChat && !ended ? { addSelection, sendLast } : undefined}
        onRefocus={() => termRef.current?.focus()}
      />
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
