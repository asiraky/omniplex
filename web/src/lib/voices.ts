import { useEffect, useSyncExternalStore } from "react";

import type { Voice } from "~/protocol";

// The ElevenLabs voice list, fetched once per page per key: General and every
// project's settings share it, and opening Settings again costs nothing.
// Storing or removing a key forgets it, since another account has other voices.

export interface VoiceList {
  voices: Voice[] | null;
  error: string | null;
}

const PENDING: VoiceList = { voices: null, error: null };

let list = PENDING;
let started = false;
// Bumped by forget, so a request already in flight for the old key cannot
// land over the new one's.
let generation = 0;
const listeners = new Set<() => void>();

function set(next: VoiceList) {
  list = next;
  for (const l of listeners) l();
}

async function fetchVoices(): Promise<Voice[]> {
  const res = await fetch("/api/tts/voices", { credentials: "same-origin" });
  const body = (await res.json().catch(() => null)) as { voices?: Voice[]; error?: string } | null;
  if (!res.ok) throw new Error(body?.error || `Couldn't load voices (${res.status})`);
  return body?.voices ?? [];
}

function start() {
  if (started) return;
  started = true;
  const mine = generation;
  fetchVoices().then(
    (voices) => mine === generation && set({ voices, error: null }),
    (e: unknown) =>
      mine === generation && set({ voices: null, error: e instanceof Error ? e.message : String(e) }),
  );
}

/** Drops the list, so the next reader fetches it again. */
export function forgetVoices() {
  generation++;
  started = false;
  set(PENDING);
}

/** After a failure: forget it and ask again. */
export function retryVoices() {
  forgetVoices();
  start();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** The voice list, loading it on first use. Nothing is fetched while `enabled` is false. */
export function useVoices(enabled = true): VoiceList {
  const current = useSyncExternalStore(subscribe, () => list, () => PENDING);
  useEffect(() => {
    if (enabled) start();
  }, [enabled, current]);
  return enabled ? current : PENDING;
}

/** A voice's accent, gender and age, the way the picker lists them. */
export function voiceTraits(v: Voice): string[] {
  const traits = ["accent", "gender", "age"]
    .map((k) => v.labels?.[k]?.replace(/_/g, " "))
    .filter((t): t is string => !!t);
  // A voice of your own is worth saying so; ElevenLabs' stock ones are the norm.
  if (v.category && v.category !== "premade") traits.push(v.category);
  return traits;
}
