import type { CSSProperties } from "react";

import { ProviderLogo } from "~/components/ProviderLogo";
import { Badge } from "~/components/ui/badge";
import { PROVIDER_LOGOS } from "~/lib/providerLogos";
import { cn } from "~/lib/utils";

/**
 * A harness the logo map knows renders as its provider's actual mark; anything
 * else falls back to the accent badge, which is what keeps a brand-new adapter
 * renderable before anyone has drawn it a logo. The accent colour still
 * travels with the harness from the server for that fallback.
 */
export function HarnessBadge({
  harness,
  accent,
  className,
}: {
  harness: string;
  accent?: string;
  className?: string;
}) {
  if (PROVIDER_LOGOS[harness]) {
    return <ProviderLogo provider={harness} className={className} />;
  }
  return (
    <Badge
      variant={accent ? "accent" : "secondary"}
      style={accent ? ({ "--badge-accent": accent } as CSSProperties) : undefined}
      className={cn(
        "rounded px-1.5 py-0.5 font-mono text-[10px] tracking-wide uppercase",
        className,
      )}
    >
      {harness}
    </Badge>
  );
}
