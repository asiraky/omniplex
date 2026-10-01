import { EllipsisIcon, KeyRoundIcon, LogOutIcon, PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon, XIcon } from "lucide-react";
import { useId, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { ConfirmDialog, DetailHeading, ErrorLine, ListRow, type PageCommand } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import {
  accountForm,
  accountName,
  accountSaveArgs,
  offersSignIn,
  serverKind,
  statusMark,
  type AccountForm,
} from "~/lib/connections";
import { formatAge } from "~/lib/usageFormat";
import { cn, errorText } from "~/lib/utils";
import type { McpServer, McpServerAccount } from "~/protocol";

import { MarkChip } from "./parts";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";
const FIELD = "h-11 font-mono md:h-8 md:text-[12px]";

/**
 * A server's further accounts. Each reaches agents as a server of its own,
 * `<server>-<label>`, so the agent can tell them apart, with its own sign-in
 * and, where it needs them, its own header or env values.
 */
export function ServerAccounts({
  server,
  command,
  onSaved,
  onSignIn,
}: {
  server: McpServer;
  command: PageCommand;
  onSaved: (s: McpServer) => void;
  /** Sign in the account agents get under this name. */
  onSignIn: (name: string) => void;
}) {
  // The label being edited, "" for a new account, null for none.
  const [editing, setEditing] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState<McpServerAccount | null>(null);
  const http = serverKind(server) === "http";

  const run = async (a: McpServerAccount, cmd: string) => {
    setBusy(a.name);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>(cmd, { name: a.name });
      if (res?.server) onSaved(res.server);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  const n = server.accounts.length;
  return (
    <section aria-label="Accounts" className="space-y-1.5">
      <DetailHeading>Accounts</DetailHeading>
      <p className="text-muted-foreground px-1 text-[12.5px] leading-snug">
        {n === 0 ? (
          <>
            Agents get this as <code className="font-mono">{server.name}</code>. Add an account to connect it a second
            time, {http ? "signed in as someone else" : "with other values"}, and agents get that as a server of its
            own.
          </>
        ) : (
          <>
            Agents get <code className="font-mono">{server.name}</code> as above, and each account below as a server of
            its own.
          </>
        )}
      </p>
      {error && <ErrorLine message={error} />}
      {n > 0 && (
        <ul className="divide-y rounded-lg border">
          {server.accounts.map((a) =>
            editing === a.label ? (
              <li key={a.label} className="px-2 py-2">
                <AccountEditor
                  server={server}
                  account={a}
                  command={command}
                  onCancel={() => setEditing(null)}
                  onSaved={(s) => {
                    setEditing(null);
                    onSaved(s);
                  }}
                />
              </li>
            ) : (
              <AccountRow
                key={a.label}
                server={server}
                account={a}
                busy={busy === a.name}
                disabled={busy !== null}
                onSignIn={() => onSignIn(a.name)}
                onCheck={() => void run(a, "check_mcp_server")}
                onSignOut={() => void run(a, "sign_out_mcp_server")}
                onEdit={() => {
                  setError("");
                  setEditing(a.label);
                }}
                onRemove={() => setRemoving(a)}
              />
            ),
          )}
        </ul>
      )}
      {editing === "" ? (
        <div className="rounded-lg border px-2 py-2">
          <AccountEditor
            server={server}
            command={command}
            onCancel={() => setEditing(null)}
            onSaved={(s, name) => {
              setEditing(null);
              onSaved(s);
              // A new account has no sign-in yet: on a remote server, that is
              // the next thing to do.
              const added = s.accounts.find((a) => a.name === name);
              if (added && offersSignIn({ url: s.url, oauth: added.oauth, status: added.status })) onSignIn(name);
            }}
          />
        </div>
      ) : (
        <Button
          variant="ghost"
          size="sm"
          className={cn(ACTION, "px-2")}
          onClick={() => {
            setError("");
            setEditing("");
          }}
        >
          <PlusIcon className="size-3.5" />
          Add account
        </Button>
      )}

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={`Remove ${removing?.name ?? ""}?`}
        description="Agents lose it from the next thread on, and its sign-in and own values are deleted."
        confirmLabel="Remove account"
        onConfirm={async () => {
          if (!removing) return;
          const res = await command<{ server?: McpServer }>("remove_mcp_account", {
            server: server.name,
            label: removing.label,
          });
          if (res?.server) onSaved(res.server);
        }}
      />
    </section>
  );
}

function accountSub(a: McpServerAccount, http: boolean): string | undefined {
  const own = http ? a.headerNames : a.envNames;
  const parts = [
    own.length > 0 ? `Own ${own.join(", ")}` : undefined,
    a.checkedAt ? `Checked ${formatAge(Date.parse(a.checkedAt), Date.now())}` : undefined,
  ].filter(Boolean);
  return parts.length ? parts.join(" · ") : undefined;
}

function AccountRow({
  server,
  account: a,
  busy,
  disabled,
  onSignIn,
  onCheck,
  onSignOut,
  onEdit,
  onRemove,
}: {
  server: McpServer;
  account: McpServerAccount;
  busy: boolean;
  disabled: boolean;
  onSignIn: () => void;
  onCheck: () => void;
  onSignOut: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const http = serverKind(server) === "http";
  const signIn = offersSignIn({ url: server.url, oauth: a.oauth, status: a.status });
  return (
    <li className="py-0.5">
      <ListRow
        title={a.name}
        markers={<MarkChip mark={a.status === "connected" ? { label: "Connected", tone: "good" } : statusMark(a.status)} />}
        sub={accountSub(a, http)}
        problem={a.error}
        foldProblem
        problemTone={a.status === "failed" ? "bad" : "attention"}
        action={
          <>
            {busy && <Spinner className="text-muted-foreground size-3.5" />}
            {signIn && (
              <Button
                size="sm"
                variant={a.status === "sign_in" ? "default" : "outline"}
                className={ACTION}
                onClick={onSignIn}
                disabled={disabled}
              >
                <KeyRoundIcon className="size-3.5" />
                Sign in
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`More for ${a.name}`}
                  className="text-muted-foreground size-11 shrink-0 md:size-8"
                  disabled={disabled}
                >
                  <EllipsisIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-40">
                {http && (
                  <DropdownMenuItem onSelect={onCheck}>
                    <RefreshCwIcon />
                    Check
                  </DropdownMenuItem>
                )}
                {http && !signIn && (
                  <DropdownMenuItem onSelect={onSignIn}>
                    <KeyRoundIcon />
                    Sign in again
                  </DropdownMenuItem>
                )}
                {a.oauth && (
                  <DropdownMenuItem onSelect={onSignOut}>
                    <LogOutIcon />
                    Sign out
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem onSelect={onEdit}>
                  <PencilIcon />
                  Edit
                </DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onSelect={onRemove}>
                  <Trash2Icon />
                  Remove
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />
    </li>
  );
}

/**
 * The label, and a value of the account's own for each header or env name
 * the server has. Left blank, the account uses the server's.
 */
function AccountEditor({
  server,
  account,
  command,
  onCancel,
  onSaved,
}: {
  server: McpServer;
  account?: McpServerAccount;
  command: PageCommand;
  onCancel: () => void;
  onSaved: (s: McpServer, name: string) => void;
}) {
  const id = useId();
  const [form, setForm] = useState<AccountForm>(() => accountForm(server, account));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const label = form.label.trim();
  const http = serverKind(server) === "http";

  const save = async () => {
    const built = accountSaveArgs(form, server, account);
    if ("error" in built) {
      setError(built.error);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>("save_mcp_account", built.args);
      if (!res?.server) throw new Error("The server did not answer with the saved account.");
      onSaved(res.server, accountName(server.name, built.args.account.label));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const setValue = (i: number, patch: Partial<AccountForm["values"][number]>) =>
    setForm((f) => ({ ...f, values: f.values.map((v, j) => (j === i ? { ...v, ...patch } : v)) }));

  return (
    <form
      className="space-y-3 px-1"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <div className="space-y-1.5">
        <Label htmlFor={id}>Label</Label>
        <Input
          id={id}
          autoFocus
          autoCapitalize="off"
          autoComplete="off"
          spellCheck={false}
          value={form.label}
          onChange={(e) => setForm((f) => ({ ...f, label: e.target.value }))}
          placeholder="work"
          className={FIELD}
        />
        <p className="text-muted-foreground text-[12px] leading-snug">
          Agents get it as <code className="font-mono">{accountName(server.name, label || "label")}</code>.
        </p>
      </div>
      {form.values.length > 0 && (
        <fieldset className="space-y-1.5">
          <legend className="mb-1.5 text-sm font-medium">{http ? "Headers" : "Environment"}</legend>
          {form.values.map((v, i) => (
            <div key={v.name} className="flex items-center gap-1.5">
              <span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{v.name}</span>
              <Input
                aria-label={`${v.name} for this account`}
                type="password"
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                value={v.value}
                placeholder={v.own ? "(own, unchanged)" : "Same as the server's"}
                onChange={(e) => setValue(i, { value: e.target.value })}
                className={cn(FIELD, "min-w-0 flex-[1.4] font-sans")}
              />
              {v.own || v.value ? (
                <IconButton label={`Use the server's ${v.name}`} onClick={() => setValue(i, { value: "", own: false })}>
                  <XIcon />
                </IconButton>
              ) : (
                <span className="size-11 shrink-0 md:size-8" />
              )}
            </div>
          ))}
          <p className="text-muted-foreground text-[12px] leading-snug">
            Blank uses the server's value. Stored values are never shown.
          </p>
        </fieldset>
      )}
      {error && <ErrorLine message={error} />}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" className={ACTION} onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" size="sm" className={ACTION} disabled={busy || !label}>
          {busy && <Spinner className="size-3.5" />}
          {account ? "Save" : "Add"}
        </Button>
      </div>
    </form>
  );
}
