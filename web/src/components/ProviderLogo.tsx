import { PROVIDER_LOGOS } from "~/lib/providerLogos";
import { cn } from "~/lib/utils";

export function ProviderLogo({
  provider,
  className,
}: {
  provider: string;
  className?: string;
}) {
  const mark = PROVIDER_LOGOS[provider];
  if (!mark) return null;
  return (
    <svg
      viewBox={mark.viewBox}
      role="img"
      aria-label={mark.label}
      fill={mark.color ?? "currentColor"}
      className={cn("size-4 shrink-0", className)}
    >
      <title>{mark.label}</title>
      <path d={mark.path} />
    </svg>
  );
}
