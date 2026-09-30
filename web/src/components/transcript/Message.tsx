import { Markdown } from "~/components/Markdown";
import type { Artefact } from "~/lib/artefacts";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";
import { useSmoothText } from "~/useSmoothText";

import type { OpenArtefact } from "./Attachments";
import { MessageMeta } from "./MessageMeta";
import { UserMessage } from "./UserMessage";

export function Message({
  item,
  threadId,
  streaming,
  recovered,
  artefacts,
  onOpenArtefact,
}: {
  item: Item;
  threadId: string;
  streaming: boolean;
  recovered?: "restart" | "continue";
  artefacts: Artefact[];
  onOpenArtefact?: OpenArtefact;
}) {
  // Paced reveal, so a harness that delivers a line at a time still reads as
  // continuous output. Inactive messages render whole.
  const text = useSmoothText(item.text ?? "", streaming);

  // The prompt that picks interrupted work back up was written by the server,
  // not by the person reading this. Showing it as their own message would be a
  // lie; so would saying the server restarted when it did not — the same
  // prompt goes out when a human continues a turn that simply failed.
  if (recovered && item.role === "user") {
    return (
      <div className="fade-in flex justify-center">
        <div className="text-muted-foreground rounded-full border px-3 py-1 text-[12px]">
          {recovered === "restart"
            ? "Server restarted — the agent was asked to pick the work back up"
            : "Asked the agent to pick the work back up"}
        </div>
      </div>
    );
  }

  if (item.role === "user") {
    return (
      <UserMessage
        item={item}
        threadId={threadId}
        artefacts={artefacts}
        onOpenArtefact={onOpenArtefact}
      />
    );
  }

  if (item.contentKind === "thought") {
    return (
      <Markdown
        text={text}
        className="fade-in text-thought border-l-2 pl-3 text-[13px] leading-relaxed break-words italic"
      />
    );
  }

  return (
    <div className="group flex flex-col gap-2">
      <Markdown
        text={text}
        className={cn(
          "fade-in text-[14px] leading-relaxed break-words",
          // The caret belongs at the end of the prose, not below it, so it
          // hangs off the last block rather than the message container.
          streaming && "caret-block",
        )}
      />
      {/* The footer arrives with the message's end: while streaming, the time
          would claim an arrival that has not happened yet. */}
      {!streaming && <MessageMeta item={item} />}
    </div>
  );
}
