import { Diff } from "~/components/Diff";
import { useDiffWrap } from "~/lib/diffWrap";

/**
 * A tool call's patch, wrapped or not by the reader's diff preference. Holding
 * the preference here, not in the card, means only a diff on screen listens
 * for it, rather than every tool card in the transcript.
 */
export function ToolDiff({ patch }: { patch: string }) {
  const [wrap] = useDiffWrap();
  return <Diff patch={patch} wrap={wrap} />;
}
