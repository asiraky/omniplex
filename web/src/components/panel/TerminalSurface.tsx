import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";

import { cn } from "~/lib/utils";

/**
 * One terminal tab: an xterm bound to a pty the server spawned in the
 * session's checkout. The shell's lifetime is this component's — closing the
 * tab (or the panel unmounting the surface) hangs up the socket and the server
 * reaps the shell. A reconnect is a fresh shell; the surface says so rather
 * than pretending continuity it does not have.
 */
export type TerminalTarget =
  /** The user's shell in a session's checkout. */
  | { session: string }
  /** A harness's own sign-in flow for one provider instance. */
  | { login: string };

export function TerminalSurface({
  target,
  onEnded,
}: {
  target: TerminalTarget;
  /** The process exited or the socket dropped. */
  onEnded?: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [gone, setGone] = useState(false);
  // Bumping this remounts the effect: a fresh socket, a fresh shell.
  const [generation, setGeneration] = useState(0);
  const targetKey = "session" in target ? `session:${target.session}` : `login:${target.login}`;
  const login = "login" in target;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setGone(false);

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
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const query =
      "session" in target
        ? `session=${encodeURIComponent(target.session)}`
        : `login=${encodeURIComponent(target.login)}`;
    const ws = new WebSocket(`${proto}//${location.host}/api/term?${query}`);
    ws.binaryType = "arraybuffer";

    let open = false;
    // Set by the cleanup below. A socket this effect hung up on itself —
    // StrictMode's double mount in development, a target change — must not
    // report the process as ended: its onclose fires after the next effect
    // has already started a fresh one.
    let disposed = false;
    ws.onopen = () => {
      open = true;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => {
      if (disposed) return;
      setGone(true);
      onEnded?.();
    };
    ws.onerror = () => ws.close();

    const data = term.onData((d) => {
      if (open && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "input", data: d }));
    });
    const resize = term.onResize(({ cols, rows }) => {
      if (open && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });

    // Refit when the panel is resized — the panel drag changes our box without
    // any window resize firing.
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(host);

    return () => {
      disposed = true;
      ro.disconnect();
      data.dispose();
      resize.dispose();
      ws.close();
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- a target is an identity, not a fresh object per render
  }, [targetKey, generation]);

  return (
    <div className="relative h-full min-h-0 bg-black/90 p-1.5">
      <div ref={hostRef} className="h-full min-h-0 [&_.xterm]:h-full" />
      {/* A finished sign-in leaves its last words on screen — "logged in as…",
          or the error — so the notice sits below rather than over them. */}
      {gone && (
        <div
          className={cn(
            "absolute flex items-center justify-center gap-3 bg-black/70 text-center",
            login ? "inset-x-0 bottom-0 flex-row px-3 py-2" : "inset-0 flex-col",
          )}
        >
          <p className="text-[13px] text-white/80">
            {login ? "The sign-in has ended." : "The shell ended or the connection dropped."}
          </p>
          <button
            type="button"
            onClick={() => setGeneration((g) => g + 1)}
            className="rounded-md border border-white/30 px-3 py-1 text-[12px] text-white/90 transition-colors hover:bg-white/10"
          >
            {login ? "Start again" : "Start a new shell"}
          </button>
        </div>
      )}
    </div>
  );
}
