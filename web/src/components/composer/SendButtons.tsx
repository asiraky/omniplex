import {
  ArrowUpIcon,
  ChevronDownIcon,
  ClockIcon,
  ListEndIcon,
  SquareIcon,
  SquarePenIcon,
  ZapIcon,
} from "lucide-react";
import { useState } from "react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuShortcut,
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

// Sends the default way, then leaves for a fresh draft in the same project,
// for firing off one task and getting straight on with the next.
function SendAndNewItem({ onSelect }: { onSelect: () => void }) {
  // Only where hints show at all, which a phone's held menu never does.
  const chord = useShortcutLabel("sendAndNewThread");
  return (
    <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={onSelect}>
      <SquarePenIcon />
      <span className="flex flex-col">
        Send and start a new thread
        <span className="text-muted-foreground text-xs">Opens a new thread here</span>
      </span>
      {chord && <DropdownMenuShortcut>{chord}</DropdownMenuShortcut>}
    </DropdownMenuItem>
  );
}

// Opened by holding Send, not by pressing it, so its trigger is only an
// anchor laid over the button, out of the pointer's way.
function HeldMenu({
  open,
  onOpenChange,
  choice,
  onSend,
  onSendAndNew,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  choice: boolean;
  onSend: (delivery?: Delivery) => void;
  onSendAndNew?: () => void;
}) {
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end">
        {choice ? (
          <DeliveryItems onSend={onSend} />
        ) : (
          // Held only for the new thread. The hold swallowed the tap that
          // would have sent, so a plain send is offered beside it.
          <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={() => onSend()}>
            <ArrowUpIcon />
            Send
          </DropdownMenuItem>
        )}
        {onSendAndNew && <SendAndNewItem onSelect={onSendAndNew} />}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// The chevron on Send's edge: the steer-or-queue choice while there is one,
// else a plain send, then sending into a new thread and scheduling.
function MoreSendOptions({
  choice,
  cannotSend,
  scheduleBlock,
  onSend,
  onSendAndNew,
  onSchedule,
}: {
  choice: boolean;
  cannotSend: boolean;
  scheduleBlock?: string;
  onSend: (delivery?: Delivery) => void;
  onSendAndNew?: () => void;
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
        {onSendAndNew && <SendAndNewItem onSelect={onSendAndNew} />}
        {onSchedule && (
          <DropdownMenuItem className="min-h-11 md:min-h-0" onSelect={onSchedule} disabled={!!scheduleBlock}>
            <ClockIcon />
            <span className="flex flex-col">
              Schedule send…
              {scheduleBlock && <span className="text-muted-foreground text-xs">{scheduleBlock}</span>}
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
  scheduleBlock,
  onSend,
  onSendAndNew,
  onSchedule,
  onCancel,
}: {
  busy: boolean;
  /** The harness can take a message mid-turn. */
  steers: boolean;
  /** Text or a sendable attachment is waiting. */
  hasContent: boolean;
  cannotSend: boolean;
  /** Why scheduling is not on offer right now, shown under it. */
  scheduleBlock?: string;
  /** Undefined sends the server's default way. */
  onSend: (delivery?: Delivery) => void;
  /** Sends the default way, then opens a new thread's draft. Only for an
      existing thread's composer. */
  onSendAndNew?: () => void;
  onSchedule?: () => void;
  onCancel: () => void;
}) {
  const stopChord = useShortcutLabel("stopTurn");
  const choice = offersChoice(busy, steers);
  const more = !!onSchedule || choice || !!onSendAndNew;

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
          <SendButton
            busy={busy}
            steers={steers}
            cannotSend={cannotSend}
            edged={more}
            onSend={onSend}
            onSendAndNew={onSendAndNew}
          />
          {more && (
            <MoreSendOptions
              choice={choice}
              cannotSend={cannotSend}
              scheduleBlock={scheduleBlock}
              onSend={onSend}
              onSendAndNew={onSendAndNew}
              onSchedule={onSchedule}
            />
          )}
        </div>
      )}
    </>
  );
}

// Send itself. A tap sends the default way; holding it, where there is a
// choice or a new thread to send into, opens the menu with the other ways.
function SendButton({
  busy,
  steers,
  cannotSend,
  edged,
  onSend,
  onSendAndNew,
}: {
  busy: boolean;
  steers: boolean;
  cannotSend: boolean;
  /** The options chevron sits against its right edge. */
  edged: boolean;
  onSend: (delivery?: Delivery) => void;
  onSendAndNew?: () => void;
}) {
  const choice = offersChoice(busy, steers);
  // A harness that cannot take a message mid-turn has only the queue.
  const fallback = SEND_LABEL[steers ? "steer" : "queue"];
  // Holding Send is the phone's way to what ⌘↵ and ⇧⌘↵ do on a keyboard.
  const holdable = choice || !!onSendAndNew;
  const [held, setHeld] = useState(false);
  const hold = useLongPress(holdable && !cannotSend, () => setHeld(true));
  // The menu goes once nothing in it could send: a turn that ends takes the
  // steer-or-queue choice with it, and the menu must not spring back open
  // when the next one starts. A turn ending under a menu that also sends into
  // a new thread leaves it open, now offering a plain send.
  if (held && (!holdable || cannotSend)) setHeld(false);

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
          holdable && "select-none [-webkit-touch-callout:none]",
        )}
      >
        <ArrowUpIcon />
      </Button>
      {holdable && (
        <HeldMenu
          open={held}
          onOpenChange={setHeld}
          choice={choice}
          onSend={onSend}
          onSendAndNew={onSendAndNew}
        />
      )}
    </span>
  );
}
