import { useEffect, useState } from "react";

/**
 * A short chime when a thread you are not looking at finishes or needs you.
 * Per device, like the diff wrap: whether a laptop on a desk should make noise
 * has nothing to do with whether the phone in your pocket should.
 */
const KEY = "omniplex.alertSound";

// Off by default. A page that starts making noise unasked is worse than one
// that stays quiet until you say otherwise.
const DEFAULT = false;

/** done: a thread finished or failed. needs: an approval or a question is waiting. */
export type AlertSound = "done" | "needs";

export function alertSoundOn(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    // Storage blocked (Safari private mode): stay quiet.
    return DEFAULT;
  }
}

function storeAlertSound(on: boolean) {
  try {
    localStorage.setItem(KEY, on ? "1" : "0");
  } catch {
    /* Not persisted. The choice still applies for this page. */
  }
}

// Every mounted switch moves together, including ones in other tabs.
const listeners = new Set<(on: boolean) => void>();

function broadcast(on: boolean) {
  for (const listener of listeners) listener(on);
}

/** The stored preference, and a setter that persists it. */
export function useAlertSound(): [boolean, (on: boolean) => void] {
  const [on, setOn] = useState(alertSoundOn);

  useEffect(() => {
    listeners.add(setOn);
    const onStorage = (e: StorageEvent) => {
      if (e.key === KEY) setOn(alertSoundOn());
    };
    window.addEventListener("storage", onStorage);
    return () => {
      listeners.delete(setOn);
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  return [
    on,
    (next: boolean) => {
      storeAlertSound(next);
      broadcast(next);
    },
  ];
}

// Two sine notes each: done rises, needs falls, so the two can be told apart
// without looking.
const NOTES: Record<AlertSound, [number, number]> = {
  done: [523.25, 783.99], // C5 up to G5
  needs: [880, 659.25], // A5 down to E5
};
const PEAK = 0.15;
const ATTACK = 0.01;
const NOTE = 0.3; // each note's length, attack to silence
const GAP = 0.12; // second note starts this long after the first
const LENGTH = GAP + NOTE;

// A resume that lands later than this is for a chime nobody is waiting for.
const RESUME_GRACE_MS = 1000;

let ctx: AudioContext | null = null;
// When each kind's current chime ends, in the context's clock.
const soundingUntil: Partial<Record<AlertSound, number>> = {};

function context(): AudioContext | null {
  if (ctx && ctx.state !== "closed") return ctx;
  ctx = null;
  try {
    const Ctor = typeof window === "undefined" ? undefined : window.AudioContext;
    if (Ctor) ctx = new Ctor();
  } catch {
    /* No audio here. */
  }
  return ctx;
}

function sound(c: AudioContext, kind: AlertSound) {
  const now = c.currentTime;
  // Several threads finishing in one broadcast chime once.
  if (now < (soundingUntil[kind] ?? -1)) return;
  soundingUntil[kind] = now + LENGTH;
  NOTES[kind].forEach((freq, i) => {
    const start = now + i * GAP;
    const osc = c.createOscillator();
    const gain = c.createGain();
    osc.type = "sine";
    osc.frequency.setValueAtTime(freq, start);
    gain.gain.setValueAtTime(0.0001, start);
    gain.gain.exponentialRampToValueAtTime(PEAK, start + ATTACK);
    gain.gain.exponentialRampToValueAtTime(0.0001, start + NOTE);
    osc.connect(gain);
    gain.connect(c.destination);
    osc.start(start);
    osc.stop(start + NOTE + 0.02);
  });
}

/** Chime, if this device has the sound on and the browser lets it. Never throws. */
export function playAlertSound(kind: AlertSound): void {
  if (!alertSoundOn()) return;
  try {
    const c = context();
    if (!c) return;
    if (c.state === "running") {
      sound(c, kind);
      return;
    }
    // Suspended: only a user gesture can wake it, so this succeeds when the
    // call is inside one (the settings switch) and quietly does nothing when
    // it is not.
    const asked = performance.now();
    c.resume().then(
      () => {
        if (c.state === "running" && performance.now() - asked < RESUME_GRACE_MS) sound(c, kind);
      },
      () => {},
    );
  } catch {
    /* A chime is never worth an error. */
  }
}

// Browsers keep audio locked until the user interacts with the page, and a
// chime fires from a server broadcast, never from a gesture. So every tap or
// key wakes the context while the sound is on. It stays registered rather than
// running once: iOS suspends the context again when the app goes to the
// background, and the next tap has to wake it again.
function unlock() {
  if (!alertSoundOn()) return;
  try {
    const c = context();
    if (c && c.state !== "running") c.resume().catch(() => {});
  } catch {
    /* No audio here. */
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("pointerdown", unlock, { capture: true, passive: true });
  window.addEventListener("keydown", unlock, { capture: true, passive: true });
}
