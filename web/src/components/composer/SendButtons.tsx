import { ArrowUpIcon, ChevronDownIcon, ClockIcon, SquareIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { cn } from "~/lib/utils";

/** The end of the toolbar: interrupt while a turn runs, and send, with
    scheduling folded into it. */
export function SendButtons({
  busy,
  hasContent,
  cannotSend,
  carriesFiles,
  onSend,
  onSchedule,
  onCancel,
}: {
  busy: boolean;
  /** Text or a sendable attachment is waiting. */
  hasContent: boolean;
  cannotSend: boolean;
  carriesFiles: boolean;
  onSend: () => void;
  onSchedule?: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      {busy && (
        <Button
          variant="destructive"
          size="icon"
          onClick={onCancel}
          aria-label="Interrupt the running turn"
          title="Interrupt the running turn"
          // Set apart from the model control beside it: the send button
          // ends the row rather than continuing it, and a shared gap made
          // the two read as one cluster.
          className="ml-1.5 size-11 shrink-0 rounded-full md:ml-2 md:size-8"
        >
          <SquareIcon className="size-3.5 fill-current" />
        </Button>
      )}
      {/* Sending while a turn runs hands the message to the harness,
          which reads it at its next step. The button only appears once
          there is something to send, so an idle-looking stop button is
          not crowded by a dead send. */}
      {(!busy || hasContent) && (
        // Scheduling rides on send's edge rather than taking its own slot:
        // a phone-width row has no room for a third round button.
        <div className="ml-1.5 flex shrink-0 md:ml-2">
          <Button
            size="icon"
            disabled={cannotSend}
            onClick={onSend}
            aria-label={busy ? "Send to the running turn" : "Send"}
            title={busy ? "The model reads it after its current step" : undefined}
            className={cn(
              "size-11 shrink-0 rounded-full md:size-8",
              onSchedule && "rounded-r-none",
            )}
          >
            <ArrowUpIcon />
          </Button>
          {onSchedule && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="icon"
                  disabled={cannotSend}
                  aria-label="More send options"
                  className="border-primary-foreground/25 h-11 w-7 shrink-0 rounded-l-none rounded-r-full border-l md:h-8 md:w-6"
                >
                  <ChevronDownIcon className="size-3.5" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="end">
                <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={onSend}>
                  <ArrowUpIcon />
                  Send now
                </DropdownMenuItem>
                {/* A scheduled prompt keeps its text and images, not files. */}
                <DropdownMenuItem
                  className="min-h-11 md:min-h-0"
                  onSelect={onSchedule}
                  disabled={carriesFiles}
                >
                  <ClockIcon />
                  <span className="flex flex-col">
                    Schedule send…
                    {carriesFiles && (
                      <span className="text-muted-foreground text-xs">Not with files attached</span>
                    )}
                  </span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      )}
    </>
  );
}
