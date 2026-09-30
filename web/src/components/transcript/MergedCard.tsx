import { GitMergeIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import type { PullRequest } from "~/protocol";

// MergedCard is the whole of the "you are probably done here" affordance: one
// quiet pill at the foot of the transcript, where the reader already is when
// the news arrives. It is not a banner and it does not nag — the transcript is
// the thing being read, so the offer sits in it and is either taken or
// scrolled past. Clicking opens the ordinary delete confirmation, which is
// where the worktree question is actually asked and answered.
export function MergedCard({ pr, onFinish }: { pr: PullRequest; onFinish: () => void }) {
  // The tooltip is the explanation, and a touch screen has none — so the
  // label states the fact and the aria-label states the offer, leaving the
  // pill legible without a hover and safe without one too: nothing is
  // destroyed until the confirmation says so.
  const offer = `Pull request #${pr.number} was merged — finish with this thread`;
  return (
    <div className="fade-in flex justify-center pt-1 pb-2">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            aria-label={offer}
            onClick={onFinish}
            className="text-muted-foreground hover:text-foreground h-11 rounded-full border border-dashed px-3 text-[12px] font-normal md:h-7"
          >
            <GitMergeIcon aria-hidden className="text-success size-3.5" />
            PR #{pr.number} merged
          </Button>
        </TooltipTrigger>
        <TooltipContent>Done with this thread? Delete it and its worktree.</TooltipContent>
      </Tooltip>
    </div>
  );
}
