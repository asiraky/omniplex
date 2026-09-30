import { parseAttachedFiles, type Artefact } from "~/lib/artefacts";
import type { QueuedPrompt } from "~/protocol";

import { PromptFiles, PromptImages } from "./Attachments";

// QueuedCard is a prompt the running turn has not read yet. It sits where it
// will land and looks like the user bubble it is about to become, dimmed, so
// the reader can see what is coming. One the harness already holds is read at
// its next step and cannot be taken back; one still waiting on the server can.
export function QueuedCard({
  queued,
  threadId,
  artefacts,
  onDequeue,
}: {
  queued: QueuedPrompt;
  threadId: string;
  artefacts: Artefact[];
  onDequeue: (queueId: string) => void;
}) {
  const { text, files } = parseAttachedFiles(queued.prompt ?? "");
  return (
    <div data-queue-id={queued.queueId} className="fade-in flex flex-col items-end">
      {queued.images && queued.images.length > 0 && (
        <div className="opacity-60">
          <PromptImages threadId={threadId} images={queued.images} />
        </div>
      )}
      {files.length > 0 && (
        <div className="flex w-full justify-end opacity-60">
          <PromptFiles files={files} artefacts={artefacts} />
        </div>
      )}
      {(text || (!queued.images?.length && files.length === 0)) && (
        <div className="bg-user-bubble text-user-bubble-foreground max-w-[85%] rounded-2xl rounded-br-md border border-dashed border-current/30 px-3.5 py-2 text-[14px] leading-relaxed break-words whitespace-pre-wrap opacity-60">
          {text}
        </div>
      )}
      <div className="text-muted-foreground mt-1 flex items-center gap-1 text-[12px]">
        {queued.sent ? (
          <span>Sent · read after the current step</span>
        ) : (
          <>
            <span>Queued</span>
            <span aria-hidden>·</span>
            <button
              type="button"
              onClick={() => onDequeue(queued.queueId)}
              className="hover:text-foreground focus-visible:ring-ring rounded-sm px-1 transition-colors outline-none focus-visible:ring-2"
            >
              Remove
            </button>
          </>
        )}
      </div>
    </div>
  );
}
