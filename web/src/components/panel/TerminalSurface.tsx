import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";

import type { OpenPath } from "~/lib/openPath";
import { bufferRows, lastTerminalLines, quoteTerminalOutput } from "~/lib/terminalQuote";
import { barKeyBytes, type BarKey, hasMods, type Mods, NO_MODS, withMods } from "~/lib/terminalKeys";
import { openTermSocket, type TermEnd, type TermSocket, type TermStatus, type TerminalTarget } from "~/lib/termSocket";
import { toast } from "~/lib/toast";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";
import { useIsCoarsePointer } from "~/useMediaQuery";

import { attachTerminalLinks } from "./attachTerminalLinks";
import { TerminalActions } from "./TerminalActions";
import { TerminalKeyBar } from "./TerminalKeyBar";

// Lines xterm keeps above the screen. The server is asked for only this many
// plus the visible rows on a fresh attach, so the two must be the same number.
const SCROLLBACK = 1000;

// How long after Enter to ask where the shell is: long enough for a cd to have
// run, and for a burst of commands to cost one question.
const CWD_SETTLE_MS = 400;

// The server's reason for a failed start when it has nothing more specific.
const GENERIC_FAILURE = "could not start";

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
 * when the server restarts. The box shell is the exception: it lives until it
 * is ended, through restarts.
 */
export function TerminalSurface({
  termId,
  target,
  onEnded,
  onAddToChat,
  onOpenPath,
  onCwd,
}: {
  /**
   * Which shell. Must be stable across remounts: StrictMode's double mount and
   * a page reload both reattach to the shell by this id.
   */
  termId: string;
  target: TerminalTarget;
  /**
   * The process ended, the server no longer has it, or it could not start.
   * reason is the server's words for it, "" when it gave none.
   */
  onEnded?: (end: TermEnd, reason: string) => void;
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
  /**
   * Where the shell is and where it started, once it connects and again after
   * each command. Without it the shell is never asked.
   */
  onCwd?: (cwd: string, root: string) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const socketRef = useRef<TermSocket | null>(null);
  const termRef = useRef<Terminal | null>(null);
  // Input to the shell, from the keyboard or the key bar. Bound to the live
  // socket while there is one.
  const sendRef = useRef<(data: string) => void>(() => {});
  const [status, setStatus] = useState<TermStatus>("connecting");
  const [ended, setEnded] = useState<{ end: TermEnd; reason: string } | null>(null);
  const [selected, setSelected] = useState(false);
  const [mods, setModsState] = useState<Mods>(NO_MODS);
  // Mirrored here rather than read from state, so a key typed straight after
  // a tap on Ctrl sees it armed before the re-render.
  const modsRef = useRef<Mods>(NO_MODS);
  const coarse = useIsCoarsePointer();
  const onEndedRef = useLatest(onEnded);
  const onOpenPathRef = useLatest(onOpenPath);
  const onCwdRef = useLatest(onCwd);
  const targetKey =
    "thread" in target ? `thread:${target.thread}` : "login" in target ? `login:${target.login}` : "box";
  const login = "login" in target;

  const setMods = (next: Mods) => {
    modsRef.current = next;
    setModsState(next);
  };

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

    let disposed = false;
    const reportCwd = () => {
      if (!onCwdRef.current) return;
      void socket.where().then((at) => {
        if (at && !disposed) onCwdRef.current?.(at.cwd, at.root);
      });
    };
    let cwdTimer: number | null = null;

    const socket = openTermSocket(termId, target, {
      onData: (bytes) => term.write(bytes),
      onReset: () => term.reset(),
      onStatus: (s) => {
        setStatus(s);
        if (s === "online") reportCwd();
      },
      onEnded: (end, reason) => {
        setEnded({ end, reason });
        onEndedRef.current?.(end, reason);
      },
    }, () => SCROLLBACK + term.rows);
    socketRef.current = socket;
    socket.resize(term.cols, term.rows);

    const links = attachTerminalLinks(term, host, {
      openPath: () => onOpenPathRef.current,
      where: () => socket.where(),
    });

    sendRef.current = (d) => {
      socket.send(d);
      // A command run may have been a cd. Asked once things settle, and only
      // by a caller that shows where the shell is.
      if (!d.includes("\r") || !onCwdRef.current) return;
      if (cwdTimer !== null) window.clearTimeout(cwdTimer);
      cwdTimer = window.setTimeout(() => {
        cwdTimer = null;
        reportCwd();
      }, CWD_SETTLE_MS);
    };

    // The phone keyboard's keys, with whatever the key bar has armed.
    const data = term.onData((d) => {
      const armed = modsRef.current;
      if (!hasMods(armed)) return sendRef.current(d);
      setMods(NO_MODS);
      sendRef.current(withMods(d, armed));
    });
    const resize = term.onResize(({ cols, rows }) => socket.resize(cols, rows));

    // Refit when the panel is resized — the panel drag changes our box without
    // any window resize firing.
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(host);

    return () => {
      disposed = true;
      if (cwdTimer !== null) window.clearTimeout(cwdTimer);
      sendRef.current = () => {};
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
    setMods(NO_MODS);
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

  const tapKey = (key: BarKey) => {
    const armed = modsRef.current;
    setMods(NO_MODS);
    sendRef.current(barKeyBytes(key, armed, termRef.current?.modes.applicationCursorKeysMode ?? false));
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

  const endedText = ended && endedMessage(ended.end, ended.reason, login);

  return (
    // Marked so Ctrl/⌘+F in here is left to the browser, not thread find.
    <div data-terminal="" className="group/terminal flex h-full min-h-0 flex-col bg-black/90">
      <div className="relative min-h-0 flex-1 p-1.5">
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
            <p className="text-[13px] text-white/80">{endedText}</p>
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
      {/* A phone keyboard has no Esc, Tab, Ctrl or arrows. Gone once the
          shell has ended, when there is nothing to send them to. */}
      {coarse && !ended && <TerminalKeyBar mods={mods} onMods={setMods} onKey={tapKey} />}
    </div>
  );
}

/**
 * The notice for an ended terminal. A failed start says why when the server
 * said something more useful than that it failed: what the user has to fix,
 * such as tmux missing on the box.
 */
function endedMessage(end: TermEnd, reason: string, login: boolean) {
  if (end === "failed" && reason && reason !== GENERIC_FAILURE) {
    return `${login ? "The sign-in" : "The shell"} could not start: ${reason}.`;
  }
  return (login ? LOGIN_ENDED : SHELL_ENDED)[end];
}
