import { useId, useState } from "react";

import { Marker, Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { canPickScope, EVERYWHERE, mcpEdits, PROJECT_SCOPE, startScope } from "~/lib/cards";
import type { Card, CardScope, CardSecret } from "~/protocol";

import { CardFrame, Code, Fact, HarnessFact, type CardControl } from "./CardFrame";

const SCOPES: { id: CardScope; label: string }[] = [
  { id: "project", label: PROJECT_SCOPE },
  { id: "everywhere", label: EVERYWHERE },
];

/** add_mcp_server: the parsed server, where it goes, and its values. */
export function McpCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const server = card.server;
  const [scope, setScope] = useState<CardScope>(() => startScope(card));
  const [env, setEnv] = useState<Record<string, string>>({});
  const [headers, setHeaders] = useState<Record<string, string>>({});

  return (
    <CardFrame card={card} ctl={ctl} accept="Save" edits={() => mcpEdits(card, { scope, env, headers })}>
      <Fact label="Server">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <Code className="text-[13px] font-medium">{server?.name}</Code>
          {card.replaces && <Marker tone="attention">replaces existing</Marker>}
        </span>
        {server?.url ? (
          <Code className="text-muted-foreground mt-1 block">{server.url}</Code>
        ) : (
          <Code className="text-muted-foreground mt-1 block">
            {[server?.command, ...(server?.args ?? [])].filter(Boolean).join(" ")}
          </Code>
        )}
      </Fact>

      {card.replaces && (
        <p className="text-attention-foreground text-[12px] leading-snug">
          A server called {server?.name} is already set up{" "}
          {scope === "project" ? "for this project" : "everywhere"}. Saving replaces it.
        </p>
      )}

      {canPickScope(card) ? (
        <div className="space-y-1.5">
          <p className="text-muted-foreground text-[11.5px] leading-none">For</p>
          <Segmented
            label="For"
            radio
            value={scope}
            options={SCOPES.map((s) =>
              s.id === "project" && card.projectName ? { ...s, label: card.projectName } : s,
            )}
            onChange={setScope}
            disabled={ctl.locked}
            className="w-full md:w-fit"
          />
          <p className="text-muted-foreground text-[11.5px] leading-snug">
            {scope === "project"
              ? "Only threads in this project get it."
              : "Every thread in every project gets it."}
          </p>
        </div>
      ) : (
        <Fact label="For">
          {EVERYWHERE}
          <span className="text-muted-foreground"> · this thread has no project</span>
        </Fact>
      )}

      <SecretFields
        legend="Headers"
        fields={server?.headers ?? []}
        values={headers}
        onChange={setHeaders}
        disabled={ctl.locked}
      />
      <SecretFields
        legend="Environment"
        fields={server?.env ?? []}
        values={env}
        onChange={setEnv}
        disabled={ctl.locked}
      />

      <HarnessFact harnesses={card.harnesses} />
    </CardFrame>
  );
}

/**
 * A field per value the server needs. One the agent passed is held by the
 * server and shown only as a mask; the user can type over it. One nobody has
 * filled is an empty box. Nothing typed here leaves the page until Save.
 */
function SecretFields({
  legend,
  fields,
  values,
  onChange,
  disabled,
}: {
  legend: string;
  fields: CardSecret[];
  values: Record<string, string>;
  onChange: (values: Record<string, string>) => void;
  disabled: boolean;
}) {
  if (fields.length === 0) return null;
  const set = (name: string, value: string | undefined) => {
    const next = { ...values };
    if (value === undefined) delete next[name];
    else next[name] = value;
    onChange(next);
  };
  return (
    <fieldset className="space-y-3">
      <legend className="text-muted-foreground mb-1.5 text-[11.5px] leading-none">{legend}</legend>
      {fields.map((f) => (
        <SecretField
          key={f.name}
          field={f}
          value={values[f.name]}
          onChange={(v) => set(f.name, v)}
          disabled={disabled}
        />
      ))}
    </fieldset>
  );
}

function SecretField({
  field,
  value,
  onChange,
  disabled,
}: {
  field: CardSecret;
  /** Undefined: untouched. */
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  disabled: boolean;
}) {
  const id = useId();
  const replacing = value !== undefined;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={field.held && !replacing ? undefined : id} className="font-mono text-[12px] font-normal break-all">
        {field.name}
      </Label>
      {field.held && !replacing ? (
        <div className="flex min-h-11 items-center gap-2 rounded-md border border-dashed px-3 md:min-h-9">
          <span aria-hidden className="font-mono text-[13px] tracking-widest">
            ••••••
          </span>
          <span className="text-muted-foreground min-w-0 flex-1 truncate text-[12px]">
            <span className="sr-only">Value hidden, </span>from the agent
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={disabled}
            aria-label={`Replace ${field.name}`}
            onClick={() => onChange("")}
            className="-mr-2 h-11 text-[12.5px] md:h-7 md:text-[12px]"
          >
            Replace
          </Button>
        </div>
      ) : (
        <div className="flex items-center gap-1.5">
          <Input
            id={id}
            type="password"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            autoFocus={field.held}
            disabled={disabled}
            value={value ?? ""}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.held ? "New value" : "Value"}
            className="min-w-0 flex-1 md:text-[12px]"
          />
          {field.held && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={disabled}
              aria-label={`Keep the agent's ${field.name}`}
              onClick={() => onChange(undefined)}
              className="h-11 shrink-0 text-[12.5px] md:h-8 md:text-[12px]"
            >
              Undo
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
