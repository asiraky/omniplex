import { ChevronDownIcon } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";

import { AttentionLabel } from "~/components/AttentionLabel";
import { IconButton } from "~/components/IconButton";
import { ErrorLine } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { CARD_LABEL } from "~/lib/cards";
import { cn, errorText } from "~/lib/utils";
import type { Card, CardEdits } from "~/protocol";

export type CardAction = "accept" | "decline";

/** resolve_card for this card. Rejects with the server's reason. */
export type ResolveCard = (action: CardAction, edits?: CardEdits) => Promise<unknown>;

/**
 * One answer in flight at a time. A failure keeps the card and says why; a
 * success keeps it locked until the resolution lands in the log and takes it
 * down, which on a slow link can be a few seconds after the reply.
 */
export function useCardResolve(resolve: ResolveCard) {
  const [busy, setBusy] = useState<CardAction | null>(null);
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async (action: CardAction, edits?: CardEdits) => {
    if (busy || done) return;
    setBusy(action);
    setError(null);
    try {
      await resolve(action, edits);
      setDone(true);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };
  return { busy, done, error, run, locked: busy !== null || done };
}

export type CardControl = ReturnType<typeof useCardResolve> & {
  open: boolean;
  setOpen: (open: boolean) => void;
  prompt: string;
  /** "2 of 3" when more than one card is waiting. */
  position?: string;
};

/**
 * The band above the composer that every card shares. It looks like the
 * permission and question cards because it is the same kind of thing, a
 * decision only the human can make, but it does not hold the turn: the
 * composer under it stays live, and the card can be folded to its one line
 * while the user keeps talking.
 */
export function CardFrame({
  card,
  ctl,
  accept,
  decline = "Decline",
  destructive = false,
  canAccept = true,
  edits,
  children,
}: {
  card: Card;
  ctl: CardControl;
  /** The affirmative button: Save, Install, Remove. */
  accept: string;
  decline?: string;
  destructive?: boolean;
  canAccept?: boolean;
  /** What the user changed, read when they accept. */
  edits?: () => CardEdits | undefined;
  children: ReactNode;
}) {
  const bodyId = useId();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (canAccept) void ctl.run("accept", edits?.());
  };

  return (
    <div className="mx-auto max-w-3xl px-4 pb-2.5 md:px-5">
      <form
        onSubmit={submit}
        aria-label={ctl.prompt}
        className="attention-in bg-card ring-attention/25 flex max-h-[60dvh] flex-col rounded-2xl border shadow-lg ring-1"
      >
        <div className="flex items-start gap-1 py-1 pr-1 pl-4">
          <div className="min-w-0 flex-1 pt-2.5 pb-1">
            <AttentionLabel label={CARD_LABEL[card.kind]}>
              {ctl.position && (
                <span className="text-muted-foreground font-mono text-[11px]">{ctl.position}</span>
              )}
            </AttentionLabel>
            <p className={cn("mt-2 text-[13px] leading-snug", ctl.open ? "text-pretty" : "truncate")}>
              {ctl.prompt}
            </p>
          </div>
          <IconButton
            label={ctl.open ? "Fold this card" : "Open this card"}
            aria-expanded={ctl.open}
            aria-controls={bodyId}
            onClick={() => ctl.setOpen(!ctl.open)}
            className="text-muted-foreground"
          >
            <ChevronDownIcon className={cn("transition-transform", !ctl.open && "rotate-180")} />
          </IconButton>
        </div>

        {ctl.open && (
          <>
            {/* The details scroll; the decision does not. A server with a
                handful of keys is taller than a phone's band, and the buttons
                are the one thing the human has to reach. */}
            <div
              id={bodyId}
              className="scroll-thin min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain px-4 pt-1 pb-4"
            >
              {children}
            </div>

            <div className="space-y-2 border-t px-4 py-3">
              {ctl.error && <ErrorLine message={ctl.error} />}
              <div className="flex gap-2 md:justify-end">
                <Button
                  type="button"
                  variant="outline"
                  disabled={ctl.locked}
                  onClick={() => void ctl.run("decline")}
                  className="min-h-11 flex-1 md:min-h-9 md:flex-none"
                >
                  {ctl.busy === "decline" && <Spinner className="size-3.5" />}
                  {decline}
                </Button>
                <Button
                  type="submit"
                  variant={destructive ? "destructive" : "default"}
                  disabled={ctl.locked || !canAccept}
                  className="min-h-11 flex-1 md:min-h-9 md:flex-none"
                >
                  {ctl.busy === "accept" && <Spinner className="size-3.5" />}
                  {accept}
                </Button>
              </div>
            </div>
          </>
        )}
      </form>
    </div>
  );
}

/** A labelled fact on a card: a caption over its value. */
export function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-1">
      <p className="text-muted-foreground text-[11.5px] leading-none">{label}</p>
      <div className="text-[13px] leading-snug">{children}</div>
    </div>
  );
}

/** Which agents get it, said once in a line. */
export function HarnessFact({ harnesses }: { harnesses?: { id: string; name: string }[] }) {
  if (!harnesses) return null;
  return (
    <Fact label="Goes to">
      {harnesses.length > 0 ? (
        harnesses.map((h) => h.name).join(", ")
      ) : (
        <span className="text-muted-foreground">No agent here can use it.</span>
      )}
    </Fact>
  );
}

/** A shell command or a path: monospace, and wrapped rather than cut off on a phone. */
export function Code({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12px] break-all", className)}>{children}</span>;
}
