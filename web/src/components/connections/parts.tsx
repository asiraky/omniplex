import { PlusIcon, XIcon } from "lucide-react";
import { useId } from "react";

import { IconButton } from "~/components/IconButton";
import { Marker, Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Switch } from "~/components/ui/switch";
import { Textarea } from "~/components/ui/textarea";
import { newRow, toggleOff, type CliForm, type Mark, type Row, type ServerForm } from "~/lib/connections";
import { cn } from "~/lib/utils";
import type { McpHarness, McpKind } from "~/protocol";

// The pieces the MCP and Sign-ins tabs share: their forms and switches.

export function MarkChip({ mark }: { mark: Mark | null }) {
  if (!mark) return null;
  return <Marker tone={mark.tone}>{mark.label}</Marker>;
}

const FIELD = "h-11 font-mono md:h-8 md:text-[12px]";

function Field({
  label,
  hint,
  value,
  onChange,
  placeholder,
  mono = true,
  inputMode,
}: {
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  inputMode?: "url";
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        inputMode={inputMode}
        autoCapitalize="off"
        autoComplete="off"
        spellCheck={false}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={cn(FIELD, !mono && "font-sans")}
      />
      {hint && <p className="text-muted-foreground text-[12px] leading-snug">{hint}</p>}
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
  addLabel,
}: {
  label: string;
  rows: Row[];
  onChange: (rows: Row[]) => void;
  secret?: boolean;
  namePlaceholder?: string;
  valuePlaceholder?: string;
  addLabel: string;
}) {
  const set = (key: number, patch: Partial<Row>) =>
    onChange(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  return (
    <fieldset className="space-y-1.5">
      <legend className="mb-1.5 text-sm font-medium">{label}</legend>
      {rows.map((row, i) => (
        <div key={row.key} className="flex items-center gap-1.5">
          <Input
            aria-label={`${label} ${i + 1} name`}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            value={row.name}
            placeholder={namePlaceholder}
            // Renaming a stored entry is a new entry: its stored value stays with the old name.
            onChange={(e) => set(row.key, { name: e.target.value, stored: false })}
            className={cn(FIELD, "min-w-0 flex-1")}
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
            className={cn(FIELD, "min-w-0 flex-[1.4]", secret && "font-sans")}
          />
          <IconButton
            label={`Remove ${row.name || "row"}`}
            onClick={() => onChange(rows.filter((r) => r.key !== row.key))}
          >
            <XIcon />
          </IconButton>
        </div>
      ))}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-11 px-2 text-[13px] md:h-8 md:text-[12px]"
        onClick={() => onChange([...rows, newRow()])}
      >
        <PlusIcon className="size-3.5" />
        {addLabel}
      </Button>
    </fieldset>
  );
}

/** One switch per agent that runs this kind of server. */
export function AgentSwitches({
  harnesses,
  off,
  disabled,
  onChange,
}: {
  harnesses: McpHarness[];
  off: string[];
  disabled?: boolean;
  onChange: (off: string[]) => void;
}) {
  const idBase = useId();
  if (harnesses.length === 0) {
    return <p className="text-muted-foreground text-[12.5px]">No agent here can run this kind of server.</p>;
  }
  return (
    <div className="divide-y rounded-lg border">
      {harnesses.map((h) => (
        <div key={h.id} className="flex min-h-11 items-center gap-2 px-3 md:min-h-10">
          <Label htmlFor={`${idBase}-${h.id}`} className="flex-1 text-[13px] font-normal">
            {h.name}
          </Label>
          <Switch
            id={`${idBase}-${h.id}`}
            checked={!off.includes(h.id)}
            disabled={disabled}
            onCheckedChange={(on) => onChange(toggleOff(off, h.id, on))}
          />
        </div>
      ))}
    </div>
  );
}

const KINDS: { id: McpKind; label: string }[] = [
  { id: "http", label: "URL" },
  { id: "stdio", label: "Command" },
];

/** The server form's fields, less which agents get it. */
export function ServerFields({
  form,
  onChange,
}: {
  form: ServerForm;
  onChange: (patch: Partial<ServerForm>) => void;
}) {
  const argsId = useId();
  const hasStored = [...form.headers, ...form.env].some((r) => r.stored);
  return (
    <div className="space-y-4">
      <Field label="Name" value={form.name} onChange={(name) => onChange({ name })} placeholder="cloudflare" />
      <div className="space-y-1.5">
        <Segmented label="How it runs" radio value={form.kind} options={KINDS} onChange={(kind) => onChange({ kind })} className="w-fit" />
      </div>
      {form.kind === "http" ? (
        <Field
          label="URL"
          inputMode="url"
          value={form.url}
          onChange={(url) => onChange({ url })}
          placeholder="https://mcp.example.com/mcp"
        />
      ) : (
        <>
          <Field label="Command" value={form.command} onChange={(command) => onChange({ command })} placeholder="npx" />
          <div className="space-y-1.5">
            <Label htmlFor={argsId}>Arguments, one per line</Label>
            <Textarea
              id={argsId}
              autoCapitalize="off"
              autoComplete="off"
              spellCheck={false}
              value={form.args}
              onChange={(e) => onChange({ args: e.target.value })}
              placeholder={"-y\n@example/mcp-server"}
              className="max-h-48 min-h-16 font-mono md:text-[12px]"
            />
          </div>
        </>
      )}
      {form.kind === "http" ? (
        <RowsEditor
          label="Headers"
          addLabel="Add header"
          secret
          rows={form.headers}
          onChange={(headers) => onChange({ headers })}
          namePlaceholder="X-Api-Key"
          valuePlaceholder="Value"
        />
      ) : (
        <RowsEditor
          label="Environment"
          addLabel="Add variable"
          secret
          rows={form.env}
          onChange={(env) => onChange({ env })}
          namePlaceholder="API_KEY"
          valuePlaceholder="Value"
        />
      )}
      {hasStored && (
        <p className="text-muted-foreground -mt-2 text-[12px] leading-snug">
          Stored values are never shown. Leave one blank to keep it, or type to replace it.
        </p>
      )}
    </div>
  );
}

/** The sign-in form's fields: the commands that check and sign in an account. */
export function CliFields({ form, onChange }: { form: CliForm; onChange: (patch: Partial<CliForm>) => void }) {
  return (
    <div className="space-y-4">
      <Field label="Name" mono={false} value={form.name} onChange={(name) => onChange({ name })} placeholder="Google Workspace" />
      <Field
        label="Status command"
        hint="Run to see whether an account is signed in."
        value={form.statusCommand}
        onChange={(statusCommand) => onChange({ statusCommand })}
        placeholder="gws auth status"
      />
      <Field
        label="Signed-in pattern"
        hint="Text the status command prints when signed in. Leave it blank and a clean exit is enough."
        value={form.signedInPattern}
        onChange={(signedInPattern) => onChange({ signedInPattern })}
      />
      <Field
        label="Sign-in command"
        hint="Prints a link to open. Paste the address your browser ends up on back here."
        value={form.signInCommand}
        onChange={(signInCommand) => onChange({ signInCommand })}
        placeholder="gws auth login"
      />
      <Field
        label="Prepare command"
        hint="Optional. Runs before signing in."
        value={form.prepareCommand}
        onChange={(prepareCommand) => onChange({ prepareCommand })}
      />
      <div className="space-y-1.5">
        <RowsEditor
          label="Account environment"
          addLabel="Add variable"
          rows={form.accountEnv}
          onChange={(accountEnv) => onChange({ accountEnv })}
          namePlaceholder="GWS_CONFIG_DIR"
          valuePlaceholder="~/.config/gws-{account}"
        />
        <p className="text-muted-foreground text-[12px] leading-snug">{"{account}"} becomes each account's name.</p>
      </div>
    </div>
  );
}
