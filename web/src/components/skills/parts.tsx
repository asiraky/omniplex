import { Marker } from "~/components/tools/parts";
import { MODE_LABEL, type SkillMode } from "~/lib/skills";

/** A row's chip, only for a skill that is not on. */
export function ModeChip({ mode }: { mode: SkillMode }) {
  if (mode === "on") return null;
  return <Marker>{MODE_LABEL[mode]}</Marker>;
}
