import { useSyncExternalStore } from "react";

import { toast } from "~/lib/toast";

// Read-aloud playback. One audio element for the whole app, so starting one
// message stops another, the lock screen has a single thing to control, and
// iOS only ever has to be talked into playing once per tap.

export type PlaybackStatus = "loading" | "playing" | null;

export interface PlayerState {
  /** What is being read: a message id, or a voice preview's key. */
  playingKey: string | null;
  status: PlaybackStatus;
}

/** The slice of HTMLAudioElement the player drives, so a test can stand one in. */
export type AudioLike = Pick<
  HTMLAudioElement,
  | "src"
  | "paused"
  | "ended"
  | "error"
  | "readyState"
  | "play"
  | "pause"
  | "load"
  | "removeAttribute"
  | "addEventListener"
>;

const IDLE: PlayerState = { playingKey: null, status: null };
const HAVE_FUTURE_DATA = 3;
const MEDIA_ERR_ABORTED = 1;

/** Where the server reads `text` aloud in the voice `threadId`'s project uses. */
export function speechURL(text: string, threadId: string): string {
  return `/api/tts?${new URLSearchParams({ text, thread: threadId })}`;
}

/**
 * Why `url` failed, asked of the server after the audio element could not
 * say. HEAD first: on a miss it answers without synthesising, so a clip that
 * failed only on the way over is not paid for twice. Only a HEAD that fails
 * is followed by a GET for the message, and the server answers a refusal it
 * just had from memory rather than asking ElevenLabs again.
 */
export async function explainFailure(url: string, fetcher: typeof fetch = fetch): Promise<string> {
  try {
    const head = await fetcher(url, { method: "HEAD", credentials: "same-origin" });
    if (head.ok) return "Couldn't play that: the connection dropped. Try again.";
    const res = await fetcher(url, { credentials: "same-origin" });
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    return body?.error || `Couldn't read that aloud (${res.status})`;
  } catch {
    return "Couldn't reach the server to read that aloud.";
  }
}

interface Current {
  key: string;
  url: string;
  /** Our own /api/tts can say why it failed; a voice preview's CDN cannot. */
  explainable: boolean;
  /** Audio has played: a failure now is the stream breaking, not a refusal. */
  started: boolean;
  artist?: string;
}

export function createPlayer({
  makeAudio = () => new Audio(),
  explain = explainFailure,
  notify = (message: string) => toast.error(message),
}: {
  makeAudio?: () => AudioLike;
  explain?: (url: string) => Promise<string>;
  notify?: (message: string) => void;
} = {}) {
  let state = IDLE;
  let audio: AudioLike | null = null;
  let current: Current | null = null;
  const listeners = new Set<() => void>();

  const set = (next: PlayerState) => {
    if (next.playingKey === state.playingKey && next.status === state.status) return;
    state = next;
    if (typeof navigator !== "undefined" && navigator.mediaSession) {
      navigator.mediaSession.playbackState = next.status ? "playing" : current ? "paused" : "none";
    }
    for (const l of listeners) l();
  };

  // Events from a src that has since been replaced still arrive, queued, after
  // the new one is set. Each handler checks the element rather than trusting
  // the event: a newer play() has already flipped `paused`, reset `readyState`
  // and cleared `error`.
  const element = () => {
    if (audio) return audio;
    const a = makeAudio();
    a.addEventListener("loadstart", () => {
      if (current && !a.paused) set({ playingKey: current.key, status: "loading" });
    });
    a.addEventListener("playing", () => {
      if (!current || a.readyState < HAVE_FUTURE_DATA) return;
      current.started = true;
      set({ playingKey: current.key, status: "playing" });
    });
    // A pause from the lock screen keeps the clip, so its play button can
    // resume it; the message's own button reads as idle meanwhile.
    a.addEventListener("pause", () => {
      if (a.paused) set(IDLE);
    });
    a.addEventListener("ended", () => {
      if (a.ended) set(IDLE);
    });
    a.addEventListener("error", () => {
      const err = a.error;
      const failed = current;
      if (!err || !failed) return;
      current = null;
      set(IDLE);
      if (err.code === MEDIA_ERR_ABORTED) return;
      if (!failed.explainable) {
        notify("Couldn't play that preview.");
      } else if (failed.started) {
        notify("Reading stopped partway: the connection dropped.");
      } else {
        void explain(failed.url).then(notify);
      }
    });
    audio = a;
    return a;
  };

  const mediaSession = (artist?: string) => {
    if (typeof navigator === "undefined" || !navigator.mediaSession) return;
    const session = navigator.mediaSession;
    if (typeof MediaMetadata !== "undefined") {
      session.metadata = new MediaMetadata({ title: "Omniplex", artist: artist ?? "" });
    }
    const handle = (action: MediaSessionAction, fn: () => void) => {
      try {
        session.setActionHandler(action, fn);
      } catch {
        // An action this browser does not know.
      }
    };
    handle("play", () => {
      if (current) void element().play().catch(() => {});
    });
    handle("pause", () => element().pause());
    handle("stop", stop);
  };

  /**
   * Plays `url` under `key`, stopping whatever was playing. Call it straight
   * from the click handler: iOS only lets audio start inside the gesture.
   */
  function playURL(key: string, url: string, opts: { explainable?: boolean; artist?: string } = {}) {
    const a = element();
    // The same clip paused from the lock screen picks up where it was.
    if (current?.url === url && a.paused && !a.ended && !a.error) {
      current.key = key;
    } else {
      current = { key, url, explainable: !!opts.explainable, started: false, artist: opts.artist };
      a.src = url;
    }
    mediaSession(current.artist);
    set({ playingKey: key, status: "loading" });
    a.play().catch((e: unknown) => {
      // AbortError is this clip being replaced or stopped; a failed load
      // arrives as an error event. Only a refusal to play at all is left.
      if (e instanceof DOMException && e.name === "NotAllowedError" && current?.url === url) {
        current = null;
        set(IDLE);
        notify("The browser blocked playback. Tap play again.");
      }
    });
  }

  /** Reads `text` aloud in the voice `threadId`'s project uses. */
  function play(key: string, text: string, threadId: string, artist?: string) {
    playURL(key, speechURL(text, threadId), { explainable: true, artist });
  }

  /** Stops and drops the clip, which also cancels a download still running. */
  function stop() {
    current = null;
    if (audio) {
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    }
    set(IDLE);
  }

  const subscribe = (l: () => void) => {
    listeners.add(l);
    return () => listeners.delete(l);
  };

  return { play, playURL, stop, subscribe, getState: () => state };
}

export type Player = ReturnType<typeof createPlayer>;

/** The app's one player. */
export const player = createPlayer();

/** Whether `key` is what the player is on, and how far along. */
export function usePlayback(key: string, p: Player = player): PlaybackStatus {
  return useSyncExternalStore(
    p.subscribe,
    () => (p.getState().playingKey === key ? p.getState().status : null),
    () => null,
  );
}
