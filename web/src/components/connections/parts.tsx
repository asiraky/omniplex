import { PlusIcon, Trash2Icon, XIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import { newRow, type Row } from "~/lib/connections";
import { cn } from "~/lib/utils";

export type Tone = "good" | "warn" | "bad";

const TONE: Record<Tone, string> = {
  good: "text-green-600 dark:text-green-500",
  warn: "text-attention-foreground",
  bad: "text-destructive",
};

/** A status in a word. Nothing at all when there is no status to report. */
export function StatusChip({ label, tone }: { label?: string; tone?: Tone }) {
  if (!label || !tone) return null;
  return (
    <Badge variant="outline" className={cn("text-[10px]", TONE[tone])}>
      {label}
    </Badge>
  );
}

export function SectionHeading({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex min-h-8 items-center gap-2">
      <h3 className="min-w-0 flex-1 text-[12px] font-medium">{children}</h3>
      {action}
    </div>
  );
}

/**
 * Name/value rows for headers and env. Secret rows are masked; a row whose
 * value is stored shows "(unchanged)" and keeps that value while left blank.
 */
export function RowsEditor({
  label,
  rows,
  onChange,
  secret,
  namePlaceholder,
  valuePlaceholder,
}: {
  label: string;
  rows: Row[];
  onChange: (rows: Row[]) => void;
  secret?: boolean;
  namePlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const idBase = useId();
  const set = (key: number, patch: Partial<Row>) =>
    onChange(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  return (
    <fieldset className="space-y-2">
      <legend className="mb-1.5 text-sm font-medium">{label}</legend>
      {rows.map((row, i) => (
        <div key={row.key} className="flex items-center gap-1.5">
          <Input
            aria-label={`${label} ${i + 1} name`}
            id={`${idBase}-${row.key}-n`}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            value={row.name}
            placeholder={namePlaceholder}
            // Renaming a stored entry is a new entry: its stored value stays with the old name.
            onChange={(e) => set(row.key, { name: e.target.value, stored: false })}
            className="min-w-0 flex-1 font-mono md:text-[12px]"
          />
          <Input
            aria-label={`${label} ${i + 1} value`}
            type={secret ? "password" : "text"}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            value={row.value}
            placeholder={row.stored ? "(unchanged)" : valuePlaceholder}
            onChange={(e) => set(row.key, { value: e.target.value })}
            className={cn("min-w-0 flex-[1.4]", !secret && "font-mono", "md:text-[12px]")}
          />
          <IconButton
            label={`Remove ${row.name || "row"}`}
            onClick={() => onChange(rows.filter((r) => r.key !== row.key))}
          >
            <XIcon />
          </IconButton>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...rows, newRow()])}>
        <PlusIcon />
        Add
      </Button>
    </fieldset>
  );
}

/** A destructive action that asks once, in place, before it runs. */
export function ConfirmRemove({
  label,
  question,
  onConfirm,
  compact,
}: {
  label: string;
  question: string;
  onConfirm: () => Promise<void>;
  /** An icon-only trigger, for a row. */
  compact?: boolean;
}) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
      setAsking(false);
    }
  };
  if (!asking) {
    return compact ? (
      <IconButton label={label} onClick={() => setAsking(true)} className="text-muted-foreground">
        <Trash2Icon />
      </IconButton>
    ) : (
      <Button
        variant="outline"
        size="sm"
        onClick={() => setAsking(true)}
        className="text-destructive hover:text-destructive"
      >
        <Trash2Icon />
        {label}
      </Button>
    );
  }
  return (
    <div className="flex w-full flex-wrap items-center gap-2">
      <span className="min-w-0 flex-1 text-[12px]">{question}</span>
      <div className="ml-auto flex gap-2">
        <Button variant="ghost" size="sm" onClick={() => setAsking(false)} disabled={busy}>
          Cancel
        </Button>
        <Button variant="destructive" size="sm" onClick={() => void run()} disabled={busy}>
          {busy ? <Spinner aria-hidden className="size-4" /> : "Remove"}
        </Button>
      </div>
    </div>
  );
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
