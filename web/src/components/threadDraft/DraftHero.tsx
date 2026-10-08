import { Wordmark } from "~/components/Logo";
import { ProviderLogo } from "~/components/ProviderLogo";

/**
 * What the empty draft shows above the composer: the wordmark on a soft glow, and
 * one quiet line naming where the thread will start. Live, so it follows the
 * chips below.
 */
export function DraftHero({
  project,
  workspace,
  harness,
  model,
}: {
  project: string;
  workspace: string;
  harness: string;
  model: string;
}) {
  const where = [project, workspace].filter(Boolean).join(" · ");
  return (
    <div className="flex flex-col items-center gap-3 [@media(max-height:560px)]:hidden">
      <div className="relative" aria-hidden>
        <div className="absolute inset-x-4 -inset-y-3 rounded-full bg-[linear-gradient(90deg,#6366f1,#a855f7,#ec4899)] opacity-20 blur-2xl" />
        <Wordmark className="text-foreground relative h-7" />
      </div>
      <p className="text-muted-foreground flex items-center gap-1.5 text-[12px]">
        <span className="max-w-[60vw] truncate">{where}</span>
        {model && (
          <>
            <span aria-hidden>·</span>
            <ProviderLogo provider={harness} className="size-3" />
            <span className="truncate">{model}</span>
          </>
        )}
      </p>
    </div>
  );
}
