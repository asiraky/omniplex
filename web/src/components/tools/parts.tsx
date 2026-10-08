import { ArrowLeftIcon, ChevronRightIcon, PlusIcon, SearchIcon, TriangleAlertIcon, XIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import { cn, errorText } from "~/lib/utils";

// The pieces every tab of the Skills page is built from: Skills, MCP servers
// and sign-ins share one look, so they share one set of parts.

export type PageCommand = <T = unknown>(name: string, args: Record<string, unknown>) => Promise<T>;

export type Tone = "quiet" | "good" | "attention" | "bad";

const MARKER_TONE: Record<Tone, string> = {
  quiet: "text-muted-foreground",
  good: "border-success/40 text-success",
  attention: "border-attention/40 bg-attention-surface text-attention-foreground",
  bad: "border-destructive/40 bg-destructive/10 text-destructive",
};

/** A small pill for the exceptions a row carries; rows with nothing to say have none. */
export function Marker({ tone = "quiet", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-1.5 py-px text-[10.5px] leading-tight font-medium whitespace-nowrap",
        MARKER_TONE[tone],
      )}
    >
      {children}
    </span>
  );
}

/** A problem as text on the page: a phone has no hover to hide it behind. */
export function ProblemText({
  problem,
  tone = "attention",
  className,
}: {
  problem?: string;
  tone?: "attention" | "bad";
  className?: string;
}) {
  if (!problem) return null;
  return (
    <span
      className={cn(
        "flex items-start gap-1.5 text-[12px] leading-snug",
        tone === "bad" ? "text-destructive" : "text-attention-foreground",
        className,
      )}
    >
      <TriangleAlertIcon className="mt-px size-3.5 shrink-0" aria-hidden />
      <span className="min-w-0 break-words">{problem}</span>
    </span>
  );
}

/**
 * A problem that can run to a page of output, folded to two lines. A tap
 * shows the rest, so it is a button of its own and cannot sit inside another.
 */
export function FoldedProblem({
  problem,
  tone = "attention",
  className,
}: {
  problem: string;
  tone?: "attention" | "bad";
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <button
      type="button"
      onClick={() => setOpen((o) => !o)}
      aria-expanded={open}
      className={cn(
        "focus-visible:ring-ring flex w-full items-start gap-1.5 rounded-md text-left text-[12px] leading-snug outline-none focus-visible:ring-2",
        tone === "bad" ? "text-destructive" : "text-attention-foreground",
        className,
      )}
    >
      <TriangleAlertIcon className="mt-px size-3.5 shrink-0" aria-hidden />
      <span className={cn("min-w-0 break-words", !open && "line-clamp-2")}>{problem}</span>
    </button>
  );
}

export function ErrorLine({ message, className }: { message: string; className?: string }) {
  return (
    <div role="alert" className={cn("text-destructive flex items-start gap-2 text-[12px]", className)}>
      <TriangleAlertIcon className="mt-px size-4 shrink-0" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  );
}

/** A centred line with a spinner, for a list that has not arrived yet. */
export function Loading({ children }: { children: ReactNode }) {
  return (
    <p className="text-muted-foreground flex items-center justify-center gap-2 px-2 py-10 text-[12.5px]">
      <Spinner className="text-primary size-3.5" /> {children}
    </p>
  );
}

/** A failed read and the one way out of it. */
export function LoadError({ message, onRetry, busy }: { message: string; onRetry: () => void; busy?: boolean }) {
  return (
    <div className="space-y-2 px-2 py-3">
      <ErrorLine message={message} />
      <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={onRetry} disabled={busy}>
        Try again
      </Button>
    </div>
  );
}

/**
 * A two-or-more-way switch in the app's pill style (see Usage). Tabs by
 * default; `radio` for a setting, where the choice is a value, not a view.
 */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  radio = false,
  disabled = false,
  className,
}: {
  label: string;
  value: T;
  options: { id: T; label: string; count?: number; attention?: boolean }[];
  onChange: (value: T) => void;
  radio?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <div
      role={radio ? "radiogroup" : "tablist"}
      aria-label={label}
      className={cn("bg-secondary/60 flex rounded-full p-0.5", className)}
    >
      {options.map((o) => {
        const selected = value === o.id;
        return (
          <button
            key={o.id}
            type="button"
            role={radio ? "radio" : "tab"}
            aria-selected={radio ? undefined : selected}
            aria-checked={radio ? selected : undefined}
            aria-label={o.attention ? `${o.label}${o.count !== undefined ? ` ${o.count}` : ""}, needs attention` : undefined}
            disabled={disabled}
            onClick={() => onChange(o.id)}
            className={cn(
              // Thumb-sized on a phone; a pointer gets the compact pill. Each
              // tab starts from its own label's width: split evenly, the longer
              // label is cut short while the row still has room to spare.
              "focus-visible:ring-ring min-h-11 min-w-0 flex-auto truncate rounded-full px-3 py-1 text-[12.5px] font-medium whitespace-nowrap outline-none focus-visible:ring-2 disabled:cursor-default md:min-h-0 md:px-2.5 md:text-[12px]",
              selected ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
            {o.count !== undefined && <span className="text-muted-foreground ml-1 tabular-nums">{o.count}</span>}
            {o.attention && <span aria-hidden className="bg-attention ml-1.5 inline-block size-1.5 rounded-full align-middle" />}
          </button>
        );
      })}
    </div>
  );
}

/** Search and Add, above every list on the page. */
export function ListToolbar({
  query,
  onQuery,
  searchLabel,
  onAdd,
}: {
  query: string;
  onQuery: (query: string) => void;
  searchLabel: string;
  onAdd: () => void;
}) {
  return (
    <div className="flex items-center gap-1.5 border-b px-2 py-1">
      <div className="relative min-w-0 flex-1">
        <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
        <input
          type="search"
          value={query}
          onChange={(e) => onQuery(e.target.value)}
          placeholder={searchLabel}
          aria-label={searchLabel}
          className="placeholder:text-muted-foreground focus-visible:ring-ring h-11 w-full rounded-md bg-transparent pr-7 pl-7 text-base outline-none focus-visible:ring-2 md:h-8 md:text-[12px] [&::-webkit-search-cancel-button]:hidden"
        />
        {query && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => onQuery("")}
            className="text-muted-foreground hover:text-foreground absolute top-1/2 right-0 flex size-11 -translate-y-1/2 items-center justify-center md:size-8"
          >
            <XIcon className="size-3.5" />
          </button>
        )}
      </div>
      <Button size="sm" className="h-11 shrink-0 text-[13px] md:h-8 md:text-[12px]" onClick={onAdd}>
        <PlusIcon className="size-3.5" />
        Add
      </Button>
    </div>
  );
}

/** A foldable group of rows with a small uppercase header. */
export function Section({
  title,
  count,
  open,
  onOpenChange,
  action,
  note,
  children,
}: {
  title: string;
  count: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Sits at the right end of the header, outside the fold control. */
  action?: ReactNode;
  /** A line under the header, shown folded or not. */
  note?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="mb-1">
      <Collapsible open={open} onOpenChange={onOpenChange}>
        <div className="flex flex-wrap items-center">
          <CollapsibleTrigger className="text-muted-foreground hover:text-foreground focus-visible:ring-ring group flex min-h-11 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-[11px] font-semibold tracking-wide uppercase outline-none focus-visible:ring-2 md:min-h-8">
            <ChevronRightIcon className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            <span className="truncate">{title}</span>
            {count > 0 && <span className="tabular-nums">{count}</span>}
          </CollapsibleTrigger>
          {action}
        </div>
        {note && <div className="text-muted-foreground px-2 pb-1 text-[12.5px] leading-snug">{note}</div>}
        <CollapsibleContent>{children}</CollapsibleContent>
      </Collapsible>
    </section>
  );
}

/**
 * One row of a list: a name, what sits beside it, a line or two under it.
 * With `onOpen` the row is the button into its detail. `action` sits at the
 * right end, outside that button; a folded problem goes under it for the same
 * reason.
 */
export function ListRow({
  title,
  mono = true,
  aside,
  markers,
  sub,
  problem,
  foldProblem,
  problemTone,
  dim,
  onOpen,
  action,
}: {
  title: string;
  mono?: boolean;
  /** Quiet text beside the name. */
  aside?: string;
  markers?: ReactNode;
  sub?: ReactNode;
  problem?: string;
  /** Long problems: folded under the row, a tap away from the rest. */
  foldProblem?: boolean;
  problemTone?: "attention" | "bad";
  dim?: boolean;
  onOpen?: () => void;
  action?: ReactNode;
}) {
  const body = (
    <>
      <span className="flex w-full min-w-0 items-center gap-1.5">
        {/* The spaces are for the button's spoken name; flex drops them on screen. */}
        <span className={cn("min-w-0 truncate", mono ? "font-mono text-[13px]" : "text-[13.5px] font-medium")}>
          {title}
        </span>{" "}
        {aside && <span className="text-muted-foreground min-w-0 truncate text-[11.5px]">{aside}</span>}{" "}
        <span className="ml-auto" />
        {markers}
      </span>{" "}
      {sub && <span className="text-muted-foreground line-clamp-2 text-[12.5px] leading-snug break-words">{sub}</span>}
      {!foldProblem && <ProblemText problem={problem} tone={problemTone} />}
    </>
  );
  const bodyClass = "flex min-h-11 w-full flex-col justify-center gap-0.5 rounded-md px-2 py-2 text-left";
  return (
    <div className={cn("flex items-center gap-1", dim && "opacity-60")}>
      <div className="min-w-0 flex-1">
        {onOpen ? (
          <button
            type="button"
            onClick={onOpen}
            className={cn(
              bodyClass,
              "hover:bg-accent/50 focus-visible:ring-ring transition-colors outline-none focus-visible:ring-2",
            )}
          >
            {body}
          </button>
        ) : (
          <div className={bodyClass}>{body}</div>
        )}
        {foldProblem && problem && <FoldedProblem problem={problem} tone={problemTone} className="-mt-1 px-2 pb-2" />}
      </div>
      {action && <div className="flex shrink-0 items-center gap-1 pr-1">{action}</div>}
    </div>
  );
}

/** The top of a detail view: back to the list, the name, where it comes from. */
export function DetailHeader({
  backLabel,
  onBack,
  title,
  mono = true,
  sub,
}: {
  backLabel: string;
  onBack: () => void;
  title: string;
  mono?: boolean;
  sub?: ReactNode;
}) {
  return (
    <div className="flex items-start gap-1 border-b px-1 py-1">
      <IconButton label={backLabel} onClick={onBack}>
        <ArrowLeftIcon />
      </IconButton>
      <div className="min-w-0 flex-1 py-1">
        <h2 className={cn("min-w-0 truncate text-[14px] font-medium", mono && "font-mono")} title={title}>
          {title}
        </h2>
        {sub && <p className="text-muted-foreground mt-0.5 text-[12px] leading-snug break-words">{sub}</p>}
      </div>
    </div>
  );
}

/** A small uppercase heading inside a detail view. */
export function DetailHeading({ children }: { children: ReactNode }) {
  return (
    <h3 className="text-muted-foreground mb-1 px-1 text-[11px] font-semibold tracking-wide uppercase">{children}</h3>
  );
}

/** Name and value pairs, read-only, in the frontmatter block's style. */
export function FactList({ facts }: { facts: [string, ReactNode][] }) {
  const shown = facts.filter(([, v]) => v !== null && v !== undefined && v !== "");
  if (shown.length === 0) return null;
  return (
    <dl className="bg-muted/40 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-lg border px-3 py-2">
      {shown.map(([key, value]) => (
        <div key={key} className="contents">
          <dt className="text-muted-foreground text-[11px] leading-5">{key}</dt>
          <dd className="min-w-0 font-mono text-[12px] leading-5 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * The editing strip under a detail view's header: Cancel and Save. Save
 * submits `formId` when there is a form, else calls `onSave`.
 */
export function EditStrip({
  label,
  saving,
  onCancel,
  formId,
  onSave,
  canSave = true,
}: {
  label: string;
  saving: boolean;
  onCancel: () => void;
  formId?: string;
  onSave?: () => void;
  canSave?: boolean;
}) {
  return (
    <div className="flex items-center gap-2 border-b px-3 py-1.5">
      <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[10.5px]">{label}</span>
      <Button variant="ghost" size="sm" className="h-11 text-[12px] md:h-8" onClick={onCancel} disabled={saving}>
        Cancel
      </Button>
      <Button
        type={formId ? "submit" : "button"}
        form={formId}
        onClick={formId ? undefined : onSave}
        size="sm"
        className="h-11 text-[12px] md:h-8"
        disabled={saving || !canSave}
      >
        {saving && <Spinner className="size-3.5" />}
        Save
      </Button>
    </div>
  );
}

/** Asks once before something is deleted, and says why it failed if it did. */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  /** Closes the dialog when it resolves; a throw keeps it open with the error. */
  onConfirm: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const run = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await onConfirm();
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (busy) return;
        if (next) setError("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {error && <ErrorLine message={error} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={() => void run()} disabled={busy}>
            {busy && <Spinner className="size-3.5" />}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A switch at the end of a list row. Its padding is part of it, so a thumb
 * finds it on a phone, and stops short of the row's own button.
 */
export function RowSwitch({
  label,
  checked,
  onCheckedChange,
  disabled,
}: {
  label: string;
  checked: boolean;
  onCheckedChange: (on: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <span className="flex h-11 items-center px-2 md:h-8">
      <Switch
        aria-label={label}
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
        className="relative after:absolute after:-inset-x-2 after:-inset-y-3.5 md:after:-inset-y-2"
      />
    </span>
  );
}
