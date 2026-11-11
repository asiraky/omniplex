import { ArrowUpIcon, ChevronDownIcon, ClockIcon, ListEndIcon, SquareIcon, ZapIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { cn } from "~/lib/utils";
import type { Delivery } from "~/protocol";
import { useShortcutLabel } from "~/useShortcuts";

import { DELIVERY_LABEL, offersChoice } from "./delivery";
import { useLongPress } from "./useLongPress";

const DELIVERY_ICON = { steer: ZapIcon, queue: ListEndIcon } satisfies Record<Delivery, unknown>;

// What Send does while a turn runs, which is the harness's default way.
const SEND_LABEL: Record<Delivery, { label: string; title: string }> = {
  steer: { label: "Steer the running turn", title: "The model reads it at its next step" },
  queue: { label: "Queue after the running turn", title: "Runs once the current turn ends" },
};

// The two ways to send mid-turn, for a harness that steers. Steering is its
// default, so it comes first and goes unsaid; only the queue is spelled out.
function DeliveryItems({ onSend }: { onSend: (delivery?: Delivery) => void }) {
  return (["steer", "queue"] as const).map((d) => {
    const Icon = DELIVERY_ICON[d];
    return (
      <DropdownMenuItem
        key={d}
        className="min-h-11 md:min-h-0"
        onSelect={() => onSend(d === "queue" ? "queue" : undefined)}
      >
        <Icon />
        <span className="flex flex-col">
          {DELIVERY_LABEL[d].name}
          <span className="text-muted-foreground text-xs">{DELIVERY_LABEL[d].detail}</span>
        </span>
      </DropdownMenuItem>
    );
  });
}

// Opened by holding Send, not by pressing it, so its trigger is only an
// anchor laid over the button, out of the pointer's way.
function HeldMenu({
  open,
  onOpenChange,
  onSend,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSend: (delivery?: Delivery) => void;
}) {
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end">
        <DeliveryItems onSend={onSend} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// The chevron on Send's edge: the steer-or-queue choice while there is one,
// else a plain send, and scheduling under either.
function MoreSendOptions({
  choice,
  cannotSend,
  carriesFiles,
  onSend,
  onSchedule,
}: {
  choice: boolean;
  cannotSend: boolean;
  carriesFiles: boolean;
  onSend: (delivery?: Delivery) => void;
  onSchedule?: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {/* A sliver of the send button's pill, too narrow for a thumb.
            On touch its target grows to 44px square, but rightwards
            only, into the composer's padding: growing from the centre
            would take taps meant for send. Hence a hand-placed
            ::after rather than `hit-44`. */}
        <Button
          size="icon"
          disabled={cannotSend}
          aria-label="More send options"
          className="border-primary-foreground/25 relative h-11 w-7 shrink-0 rounded-l-none rounded-r-full border-l pointer-coarse:after:absolute pointer-coarse:after:top-1/2 pointer-coarse:after:left-0 pointer-coarse:after:h-[max(100%,44px)] pointer-coarse:after:w-11 pointer-coarse:after:-translate-y-1/2 pointer-coarse:after:content-[''] md:h-8 md:w-6"
        >
          <ChevronDownIcon className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end">
        {choice ? (
          <DeliveryItems onSend={onSend} />
        ) : (
          <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={() => onSend()}>
            <ArrowUpIcon />
            Send now
          </DropdownMenuItem>
        )}
        {/* A scheduled prompt keeps its text and images, not files. */}
        {onSchedule && (
          <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={onSchedule} disabled={carriesFiles}>
            <ClockIcon />
            <span className="flex flex-col">
              Schedule send…
              {carriesFiles && <span className="text-muted-foreground text-xs">Not with files attached</span>}
            </span>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** The end of the toolbar: interrupt while a turn runs, and send, with
    scheduling and the steer-or-queue choice folded into it. */
export function SendButtons({
  busy,
  steers,
  hasContent,
  cannotSend,
  carriesFiles,
  onSend,
  onSchedule,
  onCancel,
}: {
  busy: boolean;
  /** The harness can take a message mid-turn. */
  steers: boolean;
  /** Text or a sendable attachment is waiting. */
  hasContent: boolean;
  cannotSend: boolean;
  carriesFiles: boolean;
  /** Undefined sends the server's default way. */
  onSend: (delivery?: Delivery) => void;
  onSchedule?: () => void;
  onCancel: () => void;
}) {
  const stopChord = useShortcutLabel("stopTurn");
  const choice = offersChoice(busy, steers);
  const more = !!onSchedule || choice;

  return (
    <>
      {busy && (
        <Button
          variant="destructive"
          size="icon"
          onClick={onCancel}
          aria-label="Interrupt the running turn"
          title={stopChord ? `Interrupt the running turn (${stopChord})` : "Interrupt the running turn"}
          // Set apart from the model control beside it: the send button
          // ends the row rather than continuing it, and a shared gap made
          // the two read as one cluster.
          className="ml-1.5 size-11 shrink-0 rounded-full md:ml-2 md:size-8"
        >
          <SquareIcon className="size-3.5 fill-current" />
        </Button>
      )}
      {/* Sending while a turn runs hands the message to the harness, which
          reads it at its next step if it can and queues it otherwise. The
          button only appears once there is something to send, so an
          idle-looking stop button is not crowded by a dead send. */}
      {(!busy || hasContent) && (
        // Scheduling rides on send's edge rather than taking its own slot:
        // a phone-width row has no room for a third round button.
        <div className="ml-1.5 flex shrink-0 md:ml-2">
          <SendButton busy={busy} steers={steers} cannotSend={cannotSend} edged={more} onSend={onSend} />
          {more && (
            <MoreSendOptions
              choice={choice}
              cannotSend={cannotSend}
              carriesFiles={carriesFiles}
              onSend={onSend}
              onSchedule={onSchedule}
            />
          )}
        </div>
      )}
    </>
  );
}

// Send itself. A tap sends the default way; holding it, where there is a
// choice, opens the menu that sends the other way.
function SendButton({
  busy,
  steers,
  cannotSend,
  edged,
  onSend,
}: {
  busy: boolean;
  steers: boolean;
  cannotSend: boolean;
  /** The options chevron sits against its right edge. */
  edged: boolean;
  onSend: (delivery?: Delivery) => void;
}) {
  const choice = offersChoice(busy, steers);
  // A harness that cannot take a message mid-turn has only the queue.
  const fallback = SEND_LABEL[steers ? "steer" : "queue"];
  // Holding Send is the phone's way to the choice ⌘↵ makes on a keyboard.
  const [held, setHeld] = useState(false);
  const hold = useLongPress(choice && !cannotSend, () => setHeld(true));
  // A turn that ends with the menu open takes the menu with it; it must not
  // spring back open when the next turn starts.
  if (held && !choice) setHeld(false);

  return (
    <span className="relative flex">
      <Button
        size="icon"
        disabled={cannotSend}
        {...hold.handlers}
        onClick={hold.click(() => onSend())}
        aria-label={busy ? fallback.label : "Send"}
        title={busy ? fallback.title : undefined}
        className={cn(
          "size-11 shrink-0 rounded-full md:size-8",
          edged && "rounded-r-none",
          // No text selection or iOS callout under a held thumb.
          choice && "select-none [-webkit-touch-callout:none]",
        )}
      >
        <ArrowUpIcon />
      </Button>
      {choice && <HeldMenu open={held} onOpenChange={setHeld} onSend={onSend} />}
    </span>
  );
}
