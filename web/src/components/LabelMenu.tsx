import { SettingsIcon } from "lucide-react";

import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
} from "~/components/ui/dropdown-menu";
import type { Label } from "~/protocol";

/** The radio value standing in for "no label": ids are uuids, so it cannot collide. */
const NONE = "none";

/** The little colour swatch that identifies a label everywhere it appears. */
export function LabelDot({ color, className }: { color: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={className ?? "size-2 shrink-0 rounded-full"}
      style={{ background: color || "var(--muted-foreground)" }}
    />
  );
}

/**
 * The assignment menu: one label or none, since a label here is a status, not
 * a tag. The same content serves the sidebar row and the session header — only
 * the trigger differs, so the trigger stays with the caller.
 */
export function LabelMenu({
  labels,
  current,
  onSelect,
  onManage,
}: {
  labels: Label[];
  /** The session's current labelId, or undefined/"" for unlabelled. */
  current?: string;
  /** Called with the new labelId, or "" to clear. */
  onSelect: (labelId: string) => void;
  onManage: () => void;
}) {
  return (
    <DropdownMenuContent align="end" className="min-w-40">
      <LabelMenuItems
        labels={labels}
        current={current}
        onSelect={onSelect}
        onManage={onManage}
      />
    </DropdownMenuContent>
  );
}

/** The assignment controls without their content wrapper, for nested menus. */
export function LabelMenuItems({
  labels,
  current,
  onSelect,
  onManage,
}: {
  labels: Label[];
  current?: string;
  onSelect: (labelId: string) => void;
  onManage: () => void;
}) {
  return (
    <>
      <DropdownMenuRadioGroup
        value={current || NONE}
        onValueChange={(v) => onSelect(v === NONE ? "" : v)}
      >
        {labels.map((l) => (
          <DropdownMenuRadioItem key={l.id} value={l.id} className="gap-2 text-[13px]">
            <LabelDot color={l.color} />
            <span className="truncate">{l.name}</span>
          </DropdownMenuRadioItem>
        ))}
        <DropdownMenuRadioItem value={NONE} className="text-muted-foreground gap-2 text-[13px]">
          No label
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuItem onSelect={onManage} className="gap-2 text-[13px]">
        <SettingsIcon className="size-3.5" />
        Manage labels…
      </DropdownMenuItem>
    </>
  );
}
