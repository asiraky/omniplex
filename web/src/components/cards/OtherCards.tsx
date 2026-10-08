import { useId, useState } from "react";

import { Marker } from "~/components/tools/parts";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Textarea } from "~/components/ui/textarea";
import { accountEdits, cliDraft, scopeText, signInEdits, type CliDraft } from "~/lib/cards";
import { cn } from "~/lib/utils";
import type { Card } from "~/protocol";

import { CardFrame, Code, Fact, HarnessFact, type CardControl } from "./CardFrame";

/** remove_mcp_server and remove_skill: what goes, and from where. */
export function RemoveCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const what = card.kind === "remove_skill" ? "Skill" : "Server";
  return (
    <CardFrame card={card} ctl={ctl} accept="Remove" decline="Keep" destructive>
      <Fact label={what}>
        <Code className="text-[13px] font-medium">{card.remove?.name}</Code>
        {card.remove?.detail && (
          <Code className="text-muted-foreground mt-1 block">{card.remove.detail}</Code>
        )}
      </Fact>
      {card.remove?.scope && <Fact label="From">{scopeText(card.remove.scope)}</Fact>}
      <HarnessFact harnesses={card.harnesses} />
    </CardFrame>
  );
}

const CLI_FIELDS: { field: Exclude<keyof CliDraft, "accountEnv" | "name">; label: string; hint?: string }[] = [
  { field: "statusCommand", label: "Status" },
  { field: "signedInPattern", label: "Signed in when", hint: "Pattern in the status output" },
  { field: "signInCommand", label: "Sign in" },
  { field: "prepareCommand", label: "Prepare" },
];

/** add_sign_in: a CLI's sign-in definition, which the user can correct before it runs. */
export function SignInCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const [draft, setDraft] = useState<CliDraft>(() => cliDraft(card.cli));
  const set = (patch: Partial<CliDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const accounts = card.cli?.accounts ?? [];
  const envKeys = Object.keys(draft.accountEnv);

  return (
    <CardFrame card={card} ctl={ctl} accept="Save" edits={() => signInEdits(card, draft)}>
      {card.cli?.id && (
        <Fact label="Id">
          <Code>{card.cli.id}</Code>
        </Fact>
      )}
      <TextField
        label="Name"
        value={draft.name}
        onChange={(name) => set({ name })}
        disabled={ctl.locked}
      />
      {/* These run in a shell on this machine, so they are shown whole and
          editable rather than summarised. */}
      <p className="text-attention-foreground text-[12px] leading-snug">
        These commands run in a shell on this machine. Read them before saving.
      </p>
      {CLI_FIELDS.map(({ field, label, hint }) => (
        <TextField
          key={field}
          label={label}
          hint={hint}
          mono
          multiline
          value={draft[field]}
          onChange={(v) => set({ [field]: v })}
          disabled={ctl.locked}
        />
      ))}
      {envKeys.length > 0 && (
        <fieldset className="space-y-3">
          <legend className="text-muted-foreground mb-1.5 text-[11.5px] leading-none">Per-account environment</legend>
          {envKeys.map((k) => (
            <TextField
              key={k}
              label={k}
              labelMono
              mono
              value={draft.accountEnv[k]}
              onChange={(v) => set({ accountEnv: { ...draft.accountEnv, [k]: v } })}
              disabled={ctl.locked}
            />
          ))}
        </fieldset>
      )}
      {accounts.length > 0 && (
        <Fact label="Accounts">
          <span className="flex flex-wrap gap-1.5">
            {accounts.map((a) => (
              <Marker key={a}>{a}</Marker>
            ))}
          </span>
        </Fact>
      )}
    </CardFrame>
  );
}

/** add_account: one more account on a sign-in, named by the user if they like. */
export function AccountCard({ card, ctl }: { card: Card; ctl: CardControl }) {
  const [name, setName] = useState(card.account?.name ?? "");
  const account = card.account;
  return (
    <CardFrame
      card={card}
      ctl={ctl}
      accept="Save"
      canAccept={name.trim() !== ""}
      edits={() => accountEdits(card, name)}
    >
      <Fact label="Sign-in">
        {account?.cliName || account?.cli}
        {account?.cliName && account.cli && <Code className="text-muted-foreground ml-1.5">{account.cli}</Code>}
      </Fact>
      <TextField label="Account" value={name} onChange={setName} disabled={ctl.locked} mono />
    </CardFrame>
  );
}

function TextField({
  label,
  hint,
  value,
  onChange,
  disabled,
  mono = false,
  labelMono = false,
  multiline = false,
}: {
  label: string;
  hint?: string;
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
  mono?: boolean;
  labelMono?: boolean;
  multiline?: boolean;
}) {
  const id = useId();
  const field = cn(mono && "font-mono md:text-[12px]");
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className={cn("text-[12px] font-normal", labelMono ? "font-mono break-all" : "text-muted-foreground")}>
        {label}
        {hint && <span className="text-muted-foreground font-normal"> · {hint}</span>}
      </Label>
      {multiline ? (
        <Textarea
          id={id}
          rows={1}
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          className={cn("min-h-11 break-all md:min-h-9", field)}
        />
      ) : (
        <Input
          id={id}
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          className={field}
        />
      )}
    </div>
  );
}
