import type { ReactNode, RefObject } from "react";

import { MicButton, RecordingStatus } from "./MicButton";
import { useDictation } from "./useDictation";

/**
 * The composer row's middle when dictation is on: the model controls and the
 * context meter, which give their room to the recording's own while it runs,
 * then the mic. Loaded on demand, so the recorder and its upload stay out of
 * the first page load for anyone without a key.
 */
export default function DictationControls({
  thread,
  textareaRef,
  draftRef,
  changeDraft,
  placeCaret,
  disabled,
  controls,
  meter,
}: {
  thread: string | undefined;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  draftRef: RefObject<string>;
  changeDraft: (next: string) => void;
  placeCaret: (cursor: number, focus: boolean) => void;
  disabled: boolean;
  /** The model picker and tools, which sit left of the spacer. */
  controls: ReactNode;
  /** The context meter, right of the spacer. */
  meter: ReactNode;
}) {
  const dictate = useDictation({ thread, textareaRef, draftRef, changeDraft, placeCaret });
  // A phone row has space for the model controls or the recording's, not both.
  const recording = dictate.status === "starting" || dictate.status === "recording";
  return (
    <>
      {recording ? <RecordingStatus elapsed={dictate.elapsed} onDiscard={dictate.discard} /> : controls}
      <span className="flex-1" />
      {!recording && meter}
      <MicButton
        status={dictate.status}
        disabled={disabled}
        onStart={() => void dictate.start()}
        onStop={dictate.stop}
      />
    </>
  );
}
