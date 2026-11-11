import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";

import {
  DICTATION_BITS_PER_SECOND,
  MAX_DICTATION_MS,
  pickMimeType,
  placeTranscript,
  transcribe,
  type DictationAnchor,
} from "~/lib/dictation";
import { toast } from "~/lib/toast";
import { useLatest } from "~/useLatest";

export type DictationStatus = "idle" | "starting" | "recording" | "transcribing";

/** One recording, from asking for the mic to the transcript landing. A
    session that is no longer `session.current` has been discarded, and
    anything it finishes afterwards is dropped. */
interface Session {
  anchor: DictationAnchor;
  thread: string | undefined;
  /** The input had focus when recording began, so it gets it back after. */
  hadFocus: boolean;
  stream?: MediaStream;
  recorder?: MediaRecorder;
  chunks: Blob[];
  abort: AbortController;
  cap?: ReturnType<typeof setTimeout>;
}

// Stopping every track is what turns the phone's mic indicator off; the
// recorder stopping alone leaves the stream, and the indicator, live.
function stopTracks(stream: MediaStream | undefined) {
  stream?.getTracks().forEach((t) => t.stop());
}

/** Tears a session down without touching React state, so it is safe from an
    unmount. */
function release(s: Session) {
  clearTimeout(s.cap);
  s.abort.abort();
  const r = s.recorder;
  if (r) {
    r.ondataavailable = null;
    r.onstop = null;
    if (r.state !== "inactive") {
      try {
        r.stop();
      } catch {
        // Already stopping; nothing left to do.
      }
    }
  }
  stopTracks(s.stream);
}

// Long enough to wait for a signal back, not so long the toast outstays it.
const RETRY_OFFERED_MS = 60_000;

function micError(err: unknown): string {
  const name = err instanceof DOMException ? err.name : "";
  if (name === "NotAllowedError" || name === "SecurityError") {
    return "Microphone blocked. Allow it for this site in the browser's settings to dictate.";
  }
  if (name === "NotFoundError" || name === "OverconstrainedError") return "No microphone found.";
  if (name === "NotReadableError") return "The microphone is busy in another app.";
  return `Couldn't start the microphone${err instanceof Error && err.message ? `: ${err.message}` : "."}`;
}

/**
 * Recording a clip, sending it off, and putting the transcript into the draft.
 *
 * The draft can change under a transcription that takes seconds, so where the
 * text lands is `placeTranscript`'s call: the selection captured when the
 * recording began if nothing was typed since, the current cursor otherwise.
 * A session belongs to one thread: switching away or unmounting throws it
 * away, upload and all, so a transcript never lands in another thread's draft.
 */
export function useDictation({
  thread,
  textareaRef,
  draftRef,
  changeDraft,
  placeCaret,
}: {
  thread: string | undefined;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  draftRef: RefObject<string>;
  changeDraft: (next: string) => void;
  /** Puts the caret after the inserted text, focusing the input only when
      `focus` says it had focus to begin with. */
  placeCaret: (cursor: number, focus: boolean) => void;
}) {
  const [status, setStatus] = useState<DictationStatus>("idle");
  const [startedAt, setStartedAt] = useState(0);
  const [now, setNow] = useState(0);
  const session = useRef<Session | null>(null);
  const latest = useLatest({ thread, changeDraft, placeCaret });
  // A failed upload offers to send the same clip again. It is set below,
  // once upload exists to call.
  const resend = useRef<(from: Session, blob: Blob) => void>(() => {});

  // The caret only means something once the person has put it somewhere. Until
  // the input has been focused its selection is whatever the browser chose
  // when the value was set, which is the start in some browsers, so dictation
  // goes on the end of the draft instead.
  const caretPlaced = useRef(false);
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    if (document.activeElement === el) caretPlaced.current = true;
    const note = () => {
      caretPlaced.current = true;
    };
    el.addEventListener("focus", note);
    return () => el.removeEventListener("focus", note);
  }, [textareaRef]);

  const caret = useCallback(() => {
    const el = textareaRef.current;
    const end = draftRef.current.length;
    if (!el || !caretPlaced.current) return { start: end, end };
    return { start: el.selectionStart, end: el.selectionEnd };
  }, [draftRef, textareaRef]);

  const end = useCallback((s: Session) => {
    if (session.current !== s) return;
    session.current = null;
    release(s);
    setStatus("idle");
  }, []);

  const land = useCallback(
    (s: Session, text: string) => {
      const el = textareaRef.current;
      const placed = placeTranscript(
        s.anchor,
        { draft: draftRef.current, cursor: caret().start },
        text,
      );
      latest.current.changeDraft(placed.value);
      latest.current.placeCaret(placed.cursor, s.hadFocus || (!!el && document.activeElement === el));
    },
    [caret, draftRef, latest, textareaRef],
  );

  const upload = useCallback(
    async (s: Session, blob: Blob) => {
      if (session.current !== s) return;
      stopTracks(s.stream);
      try {
        // An empty clip is a press too short to record anything: there is
        // nothing to send, and the answer would be silence anyway.
        const text = blob.size > 0 ? await transcribe(blob, s.thread, { signal: s.abort.signal }) : "";
        if (session.current !== s) return;
        if (text) land(s, text);
        else toast.info("Didn't catch anything. Try again a little closer to the mic.");
      } catch (err) {
        if (session.current !== s) return;
        // On a flaky connection the upload is what fails, and the person
        // should not have to say it all again: the clip is kept for a retry.
        toast.error(err instanceof Error ? err.message : String(err), {
          action: { label: "Retry", onClick: () => resend.current(s, blob) },
          duration: RETRY_OFFERED_MS,
        });
      } finally {
        end(s);
      }
    },
    [end, land],
  );

  // The retry belongs to the thread the clip was recorded in, and waits its
  // turn behind anything recording now.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    resend.current = (from, blob) => {
      if (!mounted.current || session.current || latest.current.thread !== from.thread) return;
      const s: Session = {
        anchor: from.anchor,
        thread: from.thread,
        hadFocus: from.hadFocus,
        chunks: [],
        abort: new AbortController(),
      };
      session.current = s;
      setStatus("transcribing");
      void upload(s, blob);
    };
  }, [latest, upload]);

  /** Stops recording and sends what was heard. */
  const stop = useCallback(() => {
    const s = session.current;
    const r = s?.recorder;
    if (!s || !r || r.state === "inactive") return;
    r.stop();
  }, []);

  /** Starts recording, asking for the mic the first time. */
  const start = useCallback(async () => {
    if (session.current) return;
    const el = textareaRef.current;
    const s: Session = {
      anchor: { draft: draftRef.current, ...caret() },
      thread: latest.current.thread,
      hadFocus: !!el && document.activeElement === el,
      chunks: [],
      abort: new AbortController(),
    };
    session.current = s;
    setStatus("starting");

    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (err) {
      if (session.current !== s) return;
      end(s);
      toast.error(micError(err));
      return;
    }
    s.stream = stream;
    // Discarded while the permission prompt was up: let the mic go at once.
    if (session.current !== s) {
      stopTracks(stream);
      return;
    }

    try {
      const mimeType = pickMimeType((type) => MediaRecorder.isTypeSupported(type));
      const r = new MediaRecorder(stream, {
        ...(mimeType ? { mimeType } : {}),
        audioBitsPerSecond: DICTATION_BITS_PER_SECOND,
      });
      s.recorder = r;
      r.ondataavailable = (e) => {
        if (e.data.size > 0) s.chunks.push(e.data);
      };
      // Set before starting: a recorder also stops by itself, when the mic
      // goes away mid-sentence, and what it heard until then is still sent.
      r.onstop = () => {
        clearTimeout(s.cap);
        setStatus("transcribing");
        void upload(s, new Blob(s.chunks, { type: r.mimeType || s.chunks[0]?.type || "" }));
      };
      r.start();
    } catch (err) {
      end(s);
      toast.error(`This browser can't record audio${err instanceof Error ? `: ${err.message}` : "."}`);
      return;
    }
    const t = Date.now();
    setStartedAt(t);
    setNow(t);
    setStatus("recording");
    s.cap = setTimeout(stop, MAX_DICTATION_MS);
  }, [caret, draftRef, end, latest, stop, textareaRef, upload]);

  /** Throws the recording away, or the transcription in flight, unsent. */
  const discard = useCallback(() => {
    const s = session.current;
    if (s) end(s);
  }, [end]);

  // A session is this thread's. Another thread, or none, ends it.
  useEffect(() => {
    return () => {
      const s = session.current;
      if (!s) return;
      session.current = null;
      release(s);
      setStatus("idle");
    };
  }, [thread]);

  // A quarter second is fine enough that the seconds tick over on time.
  useEffect(() => {
    if (status !== "recording") return;
    const id = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(id);
  }, [status]);

  return { status, elapsed: status === "recording" ? now - startedAt : 0, start, stop, discard };
}
