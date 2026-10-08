import { useState } from "react";

import { cardSummary } from "~/lib/cards";
import type { PendingElicitation } from "~/protocol";

import { useCardResolve, type ResolveCard } from "./CardFrame";
import { McpCard } from "./McpCard";
import { AccountCard, RemoveCard, SignInCard } from "./OtherCards";
import { CreateCard, InstallCard } from "./SkillCards";

/**
 * The card for one pending request. Key it by requestId: its edits are local
 * state that belongs to that request alone.
 */
export default function PendingCard({
  request,
  resolve,
  position,
  defaultOpen = true,
}: {
  request: PendingElicitation & { card: NonNullable<PendingElicitation["card"]> };
  resolve: ResolveCard;
  position?: string;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const resolver = useCardResolve(resolve);
  const card = request.card;
  const ctl = { ...resolver, open, setOpen, prompt: request.prompt || cardSummary(card), position };

  switch (card.kind) {
    case "add_mcp_server":
      return <McpCard card={card} ctl={ctl} />;
    case "remove_mcp_server":
    case "remove_skill":
      return <RemoveCard card={card} ctl={ctl} />;
    case "install_skill":
      return <InstallCard card={card} ctl={ctl} />;
    case "create_skill":
      return <CreateCard card={card} ctl={ctl} />;
    case "add_sign_in":
      return <SignInCard card={card} ctl={ctl} />;
    case "add_account":
      return <AccountCard card={card} ctl={ctl} />;
    default:
      return null;
  }
}
