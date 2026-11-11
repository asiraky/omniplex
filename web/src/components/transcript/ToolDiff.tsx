import { Diff } from "~/components/Diff";
import { useDiffWrap } from "~/lib/diffWrap";

/** A tool call's patch, wrapped or not by the reader's diff preference. */
export function ToolDiff({ patch }: { patch: string }) {
  const [wrap] = useDiffWrap();
  return <Diff patch={patch} wrap={wrap} />;
}
