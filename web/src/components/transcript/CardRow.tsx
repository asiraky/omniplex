import { BookOpenIcon, KeyRoundIcon, PlugIcon, UserPlusIcon, type LucideIcon } from "lucide-react";

import { Marker, type Tone } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { cardSummary, LIVE_TEXT, resultText, signInTargets, type CardSignIn } from "~/lib/cards";
import type { CardKind, CardResult, Item } from "~/protocol";

const ICON: Record<CardKind, LucideIcon> = {
  add_mcp_server: PlugIcon,
  remove_mcp_server: PlugIcon,
  install_skill: BookOpenIcon,
  create_skill: BookOpenIcon,
  remove_skill: BookOpenIcon,
  add_sign_in: KeyRoundIcon,
  add_account: UserPlusIcon,
};

const TONE: Record<CardResult, Tone> = {
  pending: "attention",
  saved: "good",
  declined: "quiet",
  cancelled: "quiet",
};

/**
 * A change the agent proposed, as one line where it happened: waiting while
 * the card above the composer is up, then what the user decided. A saved
 * server or sign-in that still needs signing in to offers it here, since this
 * is where the user is looking when they find out.
 */
export function CardRow({
  item,
  onSignIn,
}: {
  item: Item;
  onSignIn?: (target: CardSignIn) => void;
}) {
  const card = item.card;
  if (!card) return null;
  const outcome = item.outcome;
  const result = (outcome?.result ?? item.status ?? "pending") as CardResult;
  const Icon = ICON[card.kind] ?? PlugIcon;
  const targets = onSignIn ? signInTargets(outcome, window.location.origin) : [];
  const summary = outcome?.summary || cardSummary(card);

  return (
    <div className="fade-in bg-card/70 flex max-w-md flex-col gap-2 rounded-xl border px-3 py-2.5">
      <div className="flex items-start gap-2.5">
        <Icon aria-hidden className="text-muted-foreground mt-0.5 size-4 shrink-0" />
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-[13px] leading-snug text-pretty">{summary}</p>
          <span className="flex flex-wrap items-center gap-1.5">
            <Marker tone={TONE[result] ?? "quiet"}>{resultText(card.kind, result)}</Marker>
            {result === "saved" && outcome?.live && <Marker>{LIVE_TEXT[outcome.live]}</Marker>}
          </span>
        </div>
      </div>
      {targets.length > 0 && (
        <div className="flex flex-wrap gap-2 pl-6.5">
          {targets.map((t) => (
            <Button
              key={t.key}
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onSignIn?.(t)}
              className="h-11 flex-1 md:h-8 md:flex-none"
            >
              <KeyRoundIcon />
              {t.label}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}
