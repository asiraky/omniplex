import { createContext, useContext } from "react";

/**
 * The attached thread, for read-aloud: its id picks the voice (the project's,
 * else the user's) and its title names the clip on the lock screen. Provided
 * around the transcript only while an ElevenLabs key is stored; null hides
 * every play button.
 */
export interface ReadAloud {
  threadId: string;
  title: string;
}

export const ReadAloudContext = createContext<ReadAloud | null>(null);

export function useReadAloud() {
  return useContext(ReadAloudContext);
}
