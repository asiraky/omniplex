import { ArrowDownIcon, ArrowUpIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";

import { LabelDot } from "~/components/LabelMenu";
import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Input } from "~/components/ui/input";
import type { Label } from "~/protocol";

/**
 * A fixed palette rather than a free colour wheel: every entry reads against
 * both themes through the badge accent machinery, and picking from nine chips
 * is the whole decision — no contrast checking passed on to the user.
 */
export const LABEL_COLORS = [
  "#e5484d",
  "#f76b15",
  "#ffb224",
  "#46a758",
  "#12a594",
  "#0091ff",
  "#6e56cf",
  "#e93d82",
  "#8d8d8d",
];

function ColorPicker({
  value,
  onChange,
  labelName,
}: {
  value: string;
  onChange: (color: string) => void;
  labelName: string;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Colour for ${labelName}`}
        className="focus-visible:ring-ring flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md outline-none hover:bg-accent focus-visible:ring-2"
      >
        <LabelDot color={value} className="size-3 shrink-0 rounded-full" />
      </DropdownMenuTrigger>
      <DropdownMenuContent className="grid w-auto grid-cols-3 gap-1 p-2">
        {LABEL_COLORS.map((c) => (
          <button
            key={c}
            type="button"
            aria-label={`Colour ${c}`}
            aria-pressed={c === value}
            onClick={() => onChange(c)}
            className="focus-visible:ring-ring flex size-7 cursor-pointer items-center justify-center rounded-md outline-none hover:bg-accent focus-visible:ring-2"
          >
            <span
              className="size-3.5 rounded-full"
              style={{ background: c, outline: c === value ? "2px solid var(--ring)" : undefined, outlineOffset: 2 }}
            />
          </button>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Renaming commits on blur or Enter, not per keystroke: every save round-trips
 * to the server and comes back as a broadcast, and a broadcast per character
 * would fight the input for its own caret.
 */
function NameField({ label, onSave }: { label: Label; onSave: (l: Label) => void }) {
  const commit = (value: string) => {
    const name = value.trim();
    if (name && name !== label.name) onSave({ ...label, name });
  };
  return (
    <Input
      key={`${label.id}:${label.name}`}
      defaultValue={label.name}
      aria-label={`Rename ${label.name}`}
      className="h-8 flex-1 text-[13px]"
      onBlur={(e) => commit(e.currentTarget.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") {
          e.currentTarget.value = label.name;
          e.currentTarget.blur();
        }
      }}
    />
  );
}

/**
 * The one place labels are created, renamed, recoloured, reordered and
 * deleted, reached from the sidebar's label filter — there is no second label
 * button in the header. Every change goes straight to the server; the dialog
 * renders whatever the broadcast last said, so two devices editing at once
 * converge instead of diverging.
 */
export function LabelManager({
  labels,
  onCreate,
  onSave,
  onDelete,
  onClose,
}: {
  labels: Label[];
  onCreate: (name: string, color: string) => void;
  onSave: (label: Label) => void;
  onDelete: (id: string) => void;
  onClose: () => void;
}) {
  const [newName, setNewName] = useState("");
  // Rotate the palette so consecutive labels differ without anyone choosing.
  const [newColor, setNewColor] = useState(LABEL_COLORS[labels.length % LABEL_COLORS.length]);

  const create = () => {
    const name = newName.trim();
    if (!name) return;
    onCreate(name, newColor);
    setNewName("");
    setNewColor(LABEL_COLORS[(labels.length + 1) % LABEL_COLORS.length]);
  };

  // Reorder by renumbering to display indices rather than swapping the two
  // stored positions: a swap of equal positions is a no-op, and equal
  // positions can exist after two devices reordered at once. Renumbering
  // saves only the labels whose position actually changes — normally just
  // the pair — and heals any duplicates as a side effect.
  const move = (index: number, delta: -1 | 1) => {
    const other = index + delta;
    if (other < 0 || other >= labels.length) return;
    const next = [...labels];
    [next[index], next[other]] = [next[other], next[index]];
    next.forEach((l, i) => {
      if (l.position !== i) onSave({ ...l, position: i });
    });
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Labels</DialogTitle>
          <DialogDescription>
            File sessions your way. A label shows in the sidebar as its colour, and the filter
            beside it decides which ones you see. Deleting a label never deletes a session.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-1">
          {labels.map((label, i) => (
            <div key={label.id} className="flex items-center gap-1.5">
              <ColorPicker
                value={label.color}
                labelName={label.name}
                onChange={(color) => onSave({ ...label, color })}
              />
              <NameField label={label} onSave={onSave} />
              <IconButton
                label={`Move ${label.name} up`}
                disabled={i === 0}
                onClick={() => move(i, -1)}
                className="size-8"
              >
                <ArrowUpIcon />
              </IconButton>
              <IconButton
                label={`Move ${label.name} down`}
                disabled={i === labels.length - 1}
                onClick={() => move(i, 1)}
                className="size-8"
              >
                <ArrowDownIcon />
              </IconButton>
              <IconButton
                label={`Delete label ${label.name}`}
                onClick={() => onDelete(label.id)}
                className="hover:text-destructive size-8"
              >
                <Trash2Icon />
              </IconButton>
            </div>
          ))}
          {labels.length === 0 && (
            <p className="text-muted-foreground py-2 text-[13px]">
              No labels yet. Create one below and it becomes a dot on the sessions you file
              under it.
            </p>
          )}
        </div>

        <form
          className="flex items-center gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            create();
          }}
        >
          <ColorPicker value={newColor} labelName="the new label" onChange={setNewColor} />
          <Input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="New label"
            aria-label="New label name"
            className="h-8 flex-1 text-[13px]"
          />
          <Button type="submit" variant="outline" size="sm" className="h-8" disabled={!newName.trim()}>
            <PlusIcon />
            Add
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}
