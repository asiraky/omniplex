import { FileTextIcon } from "lucide-react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import type { Artefact, AttachedFile } from "~/lib/artefacts";
import { attachmentUrl, isPdf } from "~/lib/attachments";
import type { PromptImage } from "~/protocol";

export type OpenArtefact = (id: string) => void;

// The pictures and PDFs a prompt carried. Read from the attachment endpoint
// rather than from anything the event carried, so a phone attaching to a
// thread it was not in the room for sees exactly what was sent. Each tile is
// also a link: a screenshot cropped to a tile is a reminder of what was sent,
// not a look at it, and a PDF opens in the browser's own viewer.
export function PromptImages({ threadId, images }: { threadId: string; images: PromptImage[] }) {
  return (
    <div className="mb-1.5 flex max-w-[85%] flex-wrap justify-end gap-1.5">
      {images.map((image) => (
        <a
          key={image.id}
          href={attachmentUrl(threadId, image.id)}
          target="_blank"
          rel="noreferrer"
          className="focus-visible:ring-ring rounded-lg outline-none focus-visible:ring-2"
        >
          {isPdf(image.mediaType) ? (
            <span className="bg-muted text-muted-foreground flex items-center gap-1.5 rounded-lg border px-2.5 py-2 text-xs">
              <FileTextIcon className="size-4 shrink-0" />
              PDF
            </span>
          ) : (
            <img
              src={attachmentUrl(threadId, image.id)}
              alt="Attachment"
              loading="lazy"
              className="max-h-36 max-w-[9rem] rounded-lg border object-cover"
            />
          )}
        </a>
      ))}
    </div>
  );
}

// The files a prompt carried, parsed back out of the trailer the server wrote
// for the agent. The tile reads size from the thread's artefacts; a file the
// window has not loaded yet still shows, sized zero, rather than vanishing.
export function PromptFiles({
  files,
  artefacts,
  onOpen,
}: {
  files: AttachedFile[];
  artefacts: Artefact[];
  onOpen?: OpenArtefact;
}) {
  return (
    <div className="mb-1.5 flex w-full max-w-[85%] flex-col items-end gap-1.5">
      {files.map((f) => {
        const a = artefacts.find((x) => x.id === f.artefactId);
        return (
          <ArtefactTile
            key={f.artefactId}
            name={f.name}
            mediaType={f.mediaType}
            size={a?.size ?? 0}
            source="upload"
            compact
            className="w-full max-w-72"
            onOpen={onOpen && (() => onOpen(f.artefactId))}
          />
        );
      })}
    </div>
  );
}
