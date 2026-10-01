import { KeyRoundIcon, PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { FlowDialog, type AuthWires } from "~/components/AuthFlowDialog";
import { IconButton } from "~/components/IconButton";
import {
  ConfirmDialog,
  DetailHeader,
  DetailHeading,
  EditStrip,
  ErrorLine,
  FactList,
  ListRow,
  ListToolbar,
  Loading,
  LoadError,
  type PageCommand,
} from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import {
  accountMark,
  cliForm,
  cliMark,
  cliMatches,
  cliSaveArgs,
  nameProblem,
  type CliForm,
} from "~/lib/connections";
import { formatAge } from "~/lib/usageFormat";
import { cn, errorText } from "~/lib/utils";
import type { Cli, CliAccount } from "~/protocol";

import { CliFields, MarkChip } from "./parts";
import type { ConnectionsStore } from "./useConnections";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";

/**
 * The Sign-ins tab: command-line tools that keep their own sign-in, each with
 * its accounts. Omniplex runs the tool's sign-in and checks it for you.
 */
export function SignInsTab({ wires, store }: { wires: AuthWires; store: ConnectionsStore }) {
  const command = wires.command as PageCommand;
  const { conn } = store;
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [addSeq, setAddSeq] = useState(0);
  const [signIn, setSignIn] = useState<{ cli: Cli; account: string } | null>(null);
  const [afterError, setAfterError] = useState("");

  const listScrollRef = useRef<HTMLDivElement>(null);
  const savedScroll = useRef(0);
  const go = (next: string | null) => {
    if (!open && next) savedScroll.current = listScrollRef.current?.scrollTop ?? 0;
    setOpen(next);
  };
  useLayoutEffect(() => {
    if (!open && listScrollRef.current) listScrollRef.current.scrollTop = savedScroll.current;
  }, [open]);

  const clis = useMemo(
    () =>
      (conn?.clis ?? [])
        .filter((c) => cliMatches(c, query))
        .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())),
    [conn, query],
  );
  const searching = query.trim() !== "";

  const afterSignIn = async (cli: Cli) => {
    setSignIn(null);
    setAfterError("");
    try {
      const r = await command<{ cli?: Cli }>("check_cli", { id: cli.id });
      if (r?.cli) store.putCli(r.cli);
    } catch (e) {
      setAfterError(errorText(e));
    }
  };

  let detail: ReactNode = null;
  if (open && conn) {
    const cli = conn.clis.find((c) => c.id === open);
    detail = cli ? (
      <CliDetail
        key={cli.id}
        cli={cli}
        takenIds={conn.clis.map((c) => c.id)}
        command={command}
        error={afterError}
        onBack={() => go(null)}
        onSaved={(saved, previous) => {
          store.putCli(saved, previous);
          if (saved.id !== cli.id) setOpen(saved.id);
        }}
        onRemoved={(id) => {
          store.removeCli(id);
          go(null);
        }}
        onSignIn={(account) => setSignIn({ cli, account })}
      />
    ) : (
      <div className="flex h-full min-h-0 flex-col">
        <DetailHeader backLabel="Back to sign-ins" onBack={() => go(null)} title="Not found" mono={false} />
        <p className="text-muted-foreground px-4 py-10 text-center text-[12.5px]">This sign-in is no longer here.</p>
      </div>
    );
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div className={cn("flex h-full min-h-0 flex-col", open && "hidden")} aria-hidden={open ? true : undefined}>
        <ListToolbar
          query={query}
          onQuery={setQuery}
          searchLabel="Search sign-ins"
          onAdd={() => {
            setAddSeq((n) => n + 1);
            setAdding(true);
          }}
        />
        <div ref={listScrollRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
          {afterError && <ErrorLine message={afterError} className="px-2 py-2" />}
          {store.error && !conn && (
            <LoadError message={`Could not list sign-ins. ${store.error}`} onRetry={store.reload} busy={store.loading} />
          )}
          {store.loading && !conn && <Loading>Reading sign-ins…</Loading>}
          {conn && conn.clis.length === 0 && (
            <p className="text-muted-foreground px-2 py-3 text-[12.5px] leading-snug">
              No sign-ins yet. Add a command-line tool that keeps its own sign-in, and Omniplex signs in each of its
              accounts for the agents.
            </p>
          )}
          {searching && conn && clis.length === 0 && conn.clis.length > 0 && (
            <p className="text-muted-foreground px-2 py-10 text-center text-[12.5px]">No sign-ins match “{query.trim()}”.</p>
          )}
          {clis.length > 0 && (
            <ul aria-label="Sign-ins">
              {clis.map((c) => (
                <li key={c.id}>
                  <ListRow
                    title={c.name}
                    mono={false}
                    markers={<MarkChip mark={cliMark(c.accounts)} />}
                    sub={c.accounts.length ? c.accounts.map((a) => a.name).join(", ") : undefined}
                    onOpen={() => go(c.id)}
                  />
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {detail && <div className="absolute inset-0">{detail}</div>}

      {conn && (
        <AddSignInSheet
          key={addSeq}
          open={adding}
          onOpenChange={setAdding}
          command={command}
          takenIds={conn.clis.map((c) => c.id)}
          onSaved={(cli) => {
            store.putCli(cli);
            go(cli.id);
          }}
        />
      )}

      {signIn && (
        <FlowDialog
          wires={wires}
          title={`Sign in ${signIn.account}`}
          description={`${signIn.cli.name}, account ${signIn.account}`}
          begin={{ cli: signIn.cli.id, account: signIn.account }}
          onFinished={() => void afterSignIn(signIn.cli)}
          onClose={() => setSignIn(null)}
        />
      )}
    </div>
  );
}

function AccountRow({
  account,
  onSignIn,
  onRemove,
}: {
  account: CliAccount;
  onSignIn: () => void;
  onRemove: () => void;
}) {
  const problem = account.status !== "signed_in" ? account.detail : undefined;
  return (
    <li className="py-0.5">
      <ListRow
        title={account.name}
        markers={<MarkChip mark={accountMark(account.status)} />}
        sub={account.checkedAt ? `Checked ${formatAge(Date.parse(account.checkedAt), Date.now())}` : undefined}
        problem={problem}
        foldProblem
        problemTone={account.status === "failed" ? "bad" : "attention"}
        action={
          <>
            <Button
              size="sm"
              variant={account.status === "signed_in" ? "outline" : "default"}
              className={ACTION}
              onClick={onSignIn}
            >
              <KeyRoundIcon className="size-3.5" />
              Sign in
            </Button>
            <IconButton label={`Remove ${account.name}`} onClick={onRemove} className="text-muted-foreground">
              <Trash2Icon />
            </IconButton>
          </>
        }
      />
    </li>
  );
}

function AddAccount({
  cli,
  command,
  startOpen,
  onCli,
}: {
  cli: Cli;
  command: PageCommand;
  startOpen: boolean;
  onCli: (cli: Cli) => void;
}) {
  const [open, setOpen] = useState(startOpen);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const id = useId();

  if (!open) {
    return (
      <Button variant="ghost" size="sm" className={cn(ACTION, "px-2")} onClick={() => setOpen(true)}>
        <PlusIcon className="size-3.5" />
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
    setError("");
    try {
      const res = await command<{ cli?: Cli }>("add_cli_account", { id: cli.id, account });
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
      className="space-y-1.5 px-1 pt-1"
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
          className="h-11 min-w-0 flex-1 font-mono md:h-8 md:text-[12px]"
        />
        <Button type="button" variant="ghost" size="sm" className={ACTION} onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" size="sm" className={ACTION} disabled={busy || !name.trim()}>
          {busy && <Spinner className="size-3.5" />}
          Add
        </Button>
      </div>
      {error && <ErrorLine message={error} />}
    </form>
  );
}

/** One sign-in: its accounts, checking and signing them in, and its commands. */
function CliDetail({
  cli,
  takenIds,
  command,
  error: outerError,
  onBack,
  onSaved,
  onRemoved,
  onSignIn,
}: {
  cli: Cli;
  takenIds: string[];
  command: PageCommand;
  error: string;
  onBack: () => void;
  onSaved: (cli: Cli, previousId?: string) => void;
  onRemoved: (id: string) => void;
  onSignIn: (account: string) => void;
}) {
  const formId = useId();
  const [editing, setEditing] = useState<CliForm | null>(null);
  const [saving, setSaving] = useState(false);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState(false);
  const [removingAccount, setRemovingAccount] = useState<string | null>(null);

  const check = async () => {
    setChecking(true);
    setError("");
    try {
      const res = await command<{ cli?: Cli }>("check_cli", { id: cli.id });
      if (res?.cli) onSaved(res.cli);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setChecking(false);
    }
  };

  const save = async (form: CliForm) => {
    const built = cliSaveArgs(form, cli, takenIds);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const res = await command<{ cli?: Cli }>("save_cli", built.args);
      if (!res?.cli) throw new Error("The server did not answer with the saved sign-in.");
      setEditing(null);
      onSaved(res.cli, built.args.previousId);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  const header = <DetailHeader backLabel="Back to sign-ins" onBack={onBack} title={cli.name} mono={false} />;

  if (editing) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        {header}
        <EditStrip
          label={`Editing ${cli.name}`}
          formId={formId}
          saving={saving}
          onCancel={() => {
            setEditing(null);
            setError("");
          }}
        />
        {error && <ErrorLine message={error} className="border-b px-3 py-2" />}
        <form
          id={formId}
          className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            void save(editing);
          }}
        >
          <CliFields form={editing} onChange={(patch) => setEditing((f) => f && { ...f, ...patch })} />
        </form>
      </div>
    );
  }

  const n = cli.accounts.length;
  return (
    <div className="flex h-full min-h-0 flex-col">
      {header}
      <div className="scroll-thin min-h-0 flex-1 space-y-6 overflow-y-auto overscroll-contain px-3 py-3">
        <div className="space-y-3">
          {(error || outerError) && <ErrorLine message={error || outerError} />}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" className={ACTION} onClick={() => void check()} disabled={checking || n === 0}>
              {checking ? <Spinner className="size-3.5" /> : <RefreshCwIcon className="size-3.5" />}
              Check accounts
            </Button>
            <Button
              variant="outline"
              size="sm"
              className={ACTION}
              onClick={() => {
                setError("");
                setEditing(cliForm(cli));
              }}
            >
              <PencilIcon className="size-3.5" />
              Edit
            </Button>
            <Button
              variant="outline"
              size="sm"
              className={cn(ACTION, "text-destructive hover:text-destructive ml-auto")}
              onClick={() => setRemoving(true)}
            >
              <Trash2Icon className="size-3.5" />
              Remove
            </Button>
          </div>
        </div>

        <section aria-label="Accounts" className="space-y-1.5">
          <DetailHeading>Accounts</DetailHeading>
          {n > 0 ? (
            <ul className="divide-y rounded-lg border">
              {cli.accounts.map((a) => (
                <AccountRow
                  key={a.name}
                  account={a}
                  onSignIn={() => onSignIn(a.name)}
                  onRemove={() => setRemovingAccount(a.name)}
                />
              ))}
            </ul>
          ) : (
            <p className="text-muted-foreground px-1 text-[12.5px] leading-snug">
              No accounts yet. Add one per sign-in you want the agents to have.
            </p>
          )}
          <AddAccount
            cli={cli}
            command={command}
            startOpen={n === 0}
            onCli={(next) => {
              onSaved(next);
              // A new account is unchecked: ask now, so its row says where it stands.
              void check();
            }}
          />
        </section>

        <section aria-label="Details" className="space-y-1.5">
          <DetailHeading>Details</DetailHeading>
          <FactList
            facts={[
              ["Status", cli.statusCommand],
              ["Signed in when", cli.signedInPattern || undefined],
              ["Sign in", cli.signInCommand],
              ["Prepare", cli.prepareCommand || undefined],
              ...Object.entries(cli.accountEnv).map(([k, v]): [string, ReactNode] => [k, v]),
            ]}
          />
        </section>
      </div>

      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={`Remove ${cli.name}?`}
        description={
          n === 0
            ? "Omniplex stops signing in for it."
            : `Omniplex stops signing in for it and forgets its ${n === 1 ? "account" : `${n} accounts`}. The tool's own sign-in files stay where they are.`
        }
        confirmLabel="Remove sign-in"
        onConfirm={async () => {
          await command("remove_cli", { id: cli.id });
          onRemoved(cli.id);
        }}
      />
      <ConfirmDialog
        open={removingAccount !== null}
        onOpenChange={(next) => !next && setRemovingAccount(null)}
        title={`Remove ${removingAccount ?? ""}?`}
        description="Omniplex forgets this account. The tool's own sign-in files stay where they are."
        confirmLabel="Remove account"
        onConfirm={async () => {
          if (!removingAccount) return;
          const res = await command<{ cli?: Cli }>("remove_cli_account", { id: cli.id, account: removingAccount });
          if (res?.cli) onSaved(res.cli);
        }}
      />
    </div>
  );
}

/** A new sign-in: the commands that check and sign in an account. */
function AddSignInSheet({
  open,
  onOpenChange,
  command,
  takenIds,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  command: PageCommand;
  takenIds: string[];
  onSaved: (cli: Cli) => void;
}) {
  const formId = useId();
  const [form, setForm] = useState<CliForm>(() => cliForm());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const close = () => {
    if (!busy) onOpenChange(false);
  };

  const save = async () => {
    const built = cliSaveArgs(form, undefined, takenIds);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await command<{ cli?: Cli }>("save_cli", built.args);
      if (!res?.cli) throw new Error("The server did not answer with the saved sign-in.");
      onSaved(res.cli);
      onOpenChange(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent
        fullscreenOnMobile
        aria-describedby={undefined}
        className="max-md:grid-rows-[auto_minmax(0,1fr)_auto] max-md:pt-[calc(1.5rem+env(safe-area-inset-top))] md:max-h-[90dvh] md:max-w-xl md:grid-rows-[auto_minmax(0,1fr)_auto]"
      >
        <DialogHeader>
          <DialogTitle className="text-[15px]">Add a sign-in</DialogTitle>
        </DialogHeader>
        <form
          id={formId}
          className="scroll-thin -mx-1 min-h-0 space-y-4 overflow-y-auto px-1"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <CliFields form={form} onChange={(patch) => setForm((f) => ({ ...f, ...patch }))} />
          {error && <ErrorLine message={error} />}
        </form>
        <DialogFooter>
          <Button variant="outline" onClick={close} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form={formId} disabled={busy}>
            {busy && <Spinner className="size-3.5" />}
            Add sign-in
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
