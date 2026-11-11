/**
 * Dictation into the composer.
 *
 * The browser records with MediaRecorder and the server hands the clip to
 * ElevenLabs Scribe. Opus at 24 kbps is about 3 KB a second, so a sentence is
 * a few tens of kilobytes: small enough that a phone on 4G spends its time
 * waiting on the transcription, not on the upload.
 */

/** Where the draft stood when recording began: its text, and the selection
    the transcript was meant to fill. */
export interface DictationAnchor {
  draft: string;
  start: number;
  end: number;
}

/** About 3 KB a second of Opus, plenty for speech. */
export const DICTATION_BITS_PER_SECOND = 24_000;

/** A recording stops itself at this length. The server refuses much longer
    clips anyway, and a forgotten recording should not run up a bill. */
export const MAX_DICTATION_MS = 5 * 60 * 1000;

/** How long the upload and transcription may take before it is given up on.
    Past the server's own limits (a minute for Scribe, twenty seconds for the
    cleanup), so its message about what went wrong arrives first; short enough
    that a dead connection does not leave the button spinning for good. */
export const TRANSCRIBE_TIMEOUT_MS = 90_000;

// What the recorder is asked for, best first. Chrome and Firefox take Opus in
// WebM or Ogg; iOS Safari records only to MP4, which Scribe reads as well.
const MIME_PREFERENCE = ["audio/webm;codecs=opus", "audio/ogg;codecs=opus", "audio/mp4"];

const clamp = (n: number, max: number) => Math.min(Math.max(0, Math.trunc(n) || 0), max);
const WORD_START = /^[\p{L}\p{N}_]/u;

/**
 * Where a transcript lands, and where the caret goes after it.
 *
 * This is the revision guard. Transcription takes seconds, and the person may
 * keep typing meanwhile. If the draft is exactly as it was when recording
 * began, the transcript fills the selection captured then: dictating over a
 * selected phrase replaces it. If anything changed, the old offsets no longer
 * mean anything, so the transcript goes in at the current cursor and replaces
 * nothing; whatever was typed is never overwritten.
 *
 * Spacing joins it to its neighbours: a space before when it would otherwise
 * run into a word, and one after when a word follows straight on.
 */
export function placeTranscript(
  anchor: DictationAnchor,
  now: { draft: string; cursor: number },
  transcript: string,
): { value: string; cursor: number } {
  const text = transcript.trim();
  const unchanged = now.draft === anchor.draft;
  const base = now.draft;
  let start: number;
  let end: number;
  if (unchanged) {
    start = clamp(anchor.start, base.length);
    end = clamp(anchor.end, base.length);
    if (end < start) [start, end] = [end, start];
  } else {
    start = end = clamp(now.cursor, base.length);
  }
  if (!text) return { value: base, cursor: start };

  const before = base.slice(0, start);
  const after = base.slice(end);
  const lead = before && !/\s$/.test(before) ? " " : "";
  const trail = WORD_START.test(after) ? " " : "";
  const inserted = lead + text + trail;
  return { value: before + inserted + after, cursor: before.length + inserted.length };
}

/** The first recording format the browser supports, or undefined to let it
    choose its own. */
export function pickMimeType(isTypeSupported: (type: string) => boolean): string | undefined {
  return MIME_PREFERENCE.find((type) => {
    try {
      return isTypeSupported(type);
    } catch {
      return false;
    }
  });
}

/**
 * Sends a recording to the server and resolves to its transcript, which is
 * empty when nothing was heard. A failure throws the server's own message,
 * which already says what to do about it: add a key, top up, record less.
 */
export async function transcribe(
  blob: Blob,
  threadId: string | undefined,
  { signal, fetcher = fetch }: { signal?: AbortSignal; fetcher?: typeof fetch } = {},
): Promise<string> {
  const url = threadId ? `/api/dictation?thread=${encodeURIComponent(threadId)}` : "/api/dictation";
  // One controller for both the caller's abort and the timeout, built by hand:
  // AbortSignal.any is newer than the iOS Safari this has to run on.
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, TRANSCRIBE_TIMEOUT_MS);
  const forward = () => controller.abort();
  signal?.addEventListener("abort", forward);
  try {
    const res = await fetcher(url, {
      method: "POST",
      headers: { "Content-Type": blob.type || "application/octet-stream" },
      body: blob,
      signal: controller.signal,
    });
    const body = (await res.json().catch(() => ({}))) as { text?: unknown; error?: unknown };
    if (!res.ok) {
      const message = typeof body.error === "string" ? body.error : "";
      throw new Error(message || `Transcription failed (${res.status})`);
    }
    return typeof body.text === "string" ? body.text.trim() : "";
  } catch (err) {
    // The caller's own abort is not a failure to report, and a server message
    // is already the right one; only the network's own errors need rewording.
    if (signal?.aborted) throw err;
    if (timedOut) throw new Error("Transcription took too long. Try again.");
    if (err instanceof TypeError) {
      throw new Error("Couldn't reach the server to transcribe. Check the connection.");
    }
    throw err;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", forward);
  }
}

/** Elapsed recording time as m:ss. */
export function formatElapsed(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
