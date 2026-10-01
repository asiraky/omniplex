import { KeyRoundIcon, PencilIcon, PlusIcon, RefreshCwIcon } from "lucide-react";
import { useId, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import { cliForm, cliSaveArgs, nameProblem, type CliForm } from "~/lib/connections";
import { cn } from "~/lib/utils";
import type { Cli, CliAccount, CliAccountStatus } from "~/protocol";

import { ConfirmRemove, errorText, RowsEditor, SectionHeading, StatusChip, type Tone } from "./parts";
import type { Command } from "./Servers";

const CHIP: Record<CliAccountStatus, { label: string; tone: Tone } | null> = {
  signed_in: { label: "Signed in", tone: "good" },
  signed_out: { label: "Signed out", tone: "warn" },
  failed: { label: "Failed", tone: "bad" },
  unchecked: null,
};

function AccountRow({
  cli,
  account,
  first,
  command,
  onCli,
  onSignIn,
  onError,
}: {
  cli: Cli;
  account: CliAccount;
  first: boolean;
  command: Command;
  onCli: (cli: Cli) => void;
  onSignIn: () => void;
  onError: (message: string | null) => void;
}) {
  const chip = CHIP[account.status];
  const remove = async () => {
    onError(null);
    try {
      const res = (await command("remove_cli_account", { id: cli.id, account: account.name })) as { cli?: Cli };
      if (res?.cli) onCli(res.cli);
    } catch (e) {
      onError(errorText(e));
    }
  };
  return (
    <div className={cn("flex flex-wrap items-center gap-x-2 gap-y-1 py-1.5 pr-1 pl-3", !first && "border-t")}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-1.5 text-[12px] font-medium">
          {account.name}
          <StatusChip label={chip?.label} tone={chip?.tone} />
        </p>
        {account.detail && account.status !== "signed_in" && (
          <p className="text-muted-foreground text-[11px] break-words">{account.detail}</p>
        )}
      </div>
      <Button
        size="sm"
        variant={account.status === "signed_in" ? "outline" : "default"}
        onClick={onSignIn}
      >
        <KeyRoundIcon />
        Sign in
      </Button>
      <ConfirmRemove
        compact
        label={`Remove ${account.name}`}
        question={`Remove the ${account.name} account?`}
        onConfirm={remove}
      />
    </div>
  );
}

function AddAccount({
  cli,
  command,
  onCli,
}: {
  cli: Cli;
  command: Command;
  onCli: (cli: Cli) => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const id = useId();

  if (!open) {
    return (
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        <PlusIcon />
        Add account
      </Button>
    );
  }

  const add = async () => {
    const account = name.trim();
    const problem = nameProblem(account, "account");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = (await command("add_cli_account", { id: cli.id, account })) as { cli?: Cli };
      if (res?.cli) onCli(res.cli);
      setOpen(false);
      setName("");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="space-y-1.5 px-1"
      onSubmit={(e) => {
        e.preventDefault();
        void add();
      }}
    >
      <Label htmlFor={id}>Account name</Label>
      <div className="flex gap-2">
        <Input
          id={id}
          autoFocus
          autoCapitalize="off"
          autoComplete="off"
          spellCheck={false}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="work"
          className="min-w-0 flex-1 font-mono md:text-[12px]"
        />
        <Button type="button" variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy || !name.trim()}>
          Add
        </Button>
      </div>
      {error && <p className="text-destructive text-[12px]">{error}</p>}
    </form>
  );
}

/** One command-line tool and its accounts. */
function CliGroup({
  cli,
  command,
  onCli,
  onEdit,
  onSignIn,
  onError,
}: {
  cli: Cli;
  command: Command;
  onCli: (cli: Cli) => void;
  onEdit: () => void;
  onSignIn: (account: string) => void;
  onError: (message: string | null) => void;
}) {
  const [checking, setChecking] = useState(false);
  const check = async () => {
    setChecking(true);
    onError(null);
    try {
      const res = (await command("check_cli", { id: cli.id })) as { cli?: Cli };
      if (res?.cli) onCli(res.cli);
    } catch (e) {
      onError(errorText(e));
    } finally {
      setChecking(false);
    }
  };
  return (
    <div className="space-y-1">
      <div className="flex items-center gap-1">
        <p className="min-w-0 flex-1 truncate text-[12px] font-medium">{cli.name}</p>
        <IconButton label={`Check ${cli.name} accounts`} onClick={() => void check()} disabled={checking}>
          {checking ? <Spinner aria-hidden className="size-4" /> : <RefreshCwIcon />}
        </IconButton>
        <IconButton label={`Edit ${cli.name}`} onClick={onEdit}>
          <PencilIcon />
        </IconButton>
      </div>
      <div className="overflow-hidden rounded-lg border">
        {cli.accounts.map((a, i) => (
          <AccountRow
            key={a.name}
            cli={cli}
            account={a}
            first={i === 0}
            command={command}
            onCli={onCli}
            onSignIn={() => onSignIn(a.name)}
            onError={onError}
          />
        ))}
        {cli.accounts.length === 0 && (
          <p className="text-muted-foreground px-3 py-2.5 text-[11px]">No accounts yet.</p>
        )}
      </div>
      <AddAccount cli={cli} command={command} onCli={onCli} />
    </div>
  );
}

/** Sign-ins: every command-line tool Omniplex signs in for, grouped. */
export function SignInList({
  clis,
  command,
  onCli,
  onEdit,
  onAdd,
  onSignIn,
  onError,
}: {
  clis: Cli[];
  command: Command;
  onCli: (cli: Cli) => void;
  onEdit: (id: string) => void;
  onAdd: () => void;
  onSignIn: (cli: Cli, account: string) => void;
  onError: (message: string | null) => void;
}) {
  return (
    <div className="space-y-3">
      <SectionHeading
        action={
          <Button size="sm" variant="outline" onClick={onAdd}>
            Add sign-in
          </Button>
        }
      >
        Sign-ins
      </SectionHeading>
      {clis.length === 0 && (
        <p className="text-muted-foreground text-[11px]">
          Command-line tools that keep their own sign-in, one per account.
        </p>
      )}
      {clis.map((c) => (
        <CliGroup
          key={c.id}
          cli={c}
          command={command}
          onCli={onCli}
          onEdit={() => onEdit(c.id)}
          onSignIn={(account) => onSignIn(c, account)}
          onError={onError}
        />
      ))}
    </div>
  );
}

function Field({
  label,
  hint,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        autoCapitalize="off"
        autoComplete="off"
        spellCheck={false}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="font-mono md:text-[12px]"
      />
      {hint && <p className="text-muted-foreground text-[11px]">{hint}</p>}
    </div>
  );
}

/** Add or edit one sign-in: the commands that check and sign in an account. */
export function CliFormView({
  existing,
  takenIds,
  command,
  onSaved,
  onRemoved,
  onCancel,
}: {
  existing?: Cli;
  takenIds: string[];
  command: Command;
  onSaved: (cli: Cli, previousId?: string) => void;
  onRemoved: (id: string) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<CliForm>(() => cliForm(existing));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (patch: Partial<CliForm>) => setForm((f) => ({ ...f, ...patch }));

  const save = async () => {
    const built = cliSaveArgs(form, existing, takenIds);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = (await command("save_cli", built.args)) as { cli?: Cli };
      if (!res?.cli) throw new Error("The server did not answer with the saved sign-in.");
      onSaved(res.cli, built.args.previousId);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!existing) return;
    setError(null);
    try {
      await command("remove_cli", { id: existing.id });
      onRemoved(existing.id);
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <div className="space-y-5">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Field label="Name" value={form.name} onChange={(name) => set({ name })} placeholder="Google Workspace" />
        <Field
          label="Status command"
          hint="Run to see whether an account is signed in."
          value={form.statusCommand}
          onChange={(statusCommand) => set({ statusCommand })}
          placeholder="gws auth status"
        />
        <Field
          label="Signed-in pattern"
          hint="Text the status command prints when signed in. Blank means a clean exit is enough."
          value={form.signedInPattern}
          onChange={(signedInPattern) => set({ signedInPattern })}
        />
        <Field
          label="Sign-in command"
          hint="Prints a link to open. Paste the address your browser ends up on back here."
          value={form.signInCommand}
          onChange={(signInCommand) => set({ signInCommand })}
          placeholder="gws auth login"
        />
        <Field
          label="Prepare command"
          hint="Optional. Runs before signing in."
          value={form.prepareCommand}
          onChange={(prepareCommand) => set({ prepareCommand })}
        />
        <RowsEditor
          label="Account environment"
          rows={form.accountEnv}
          onChange={(accountEnv) => set({ accountEnv })}
          namePlaceholder="GWS_CONFIG_DIR"
          valuePlaceholder="~/.config/gws-{account}"
        />
        <p className="text-muted-foreground -mt-2 text-[11px]">
          {"{account}"} becomes each account's name.
        </p>

        {error && (
          <Alert variant="destructive">
            <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy}>
            {busy ? <Spinner aria-hidden className="size-4" /> : null}
            {existing ? "Save" : "Add sign-in"}
          </Button>
        </div>
      </form>

      {existing && (
        <>
          <Separator />
          <ConfirmRemove
            label="Remove sign-in"
            question={`Remove ${existing.name} and its ${existing.accounts.length} account${existing.accounts.length === 1 ? "" : "s"}?`}
            onConfirm={remove}
          />
        </>
      )}
    </div>
  );
}
