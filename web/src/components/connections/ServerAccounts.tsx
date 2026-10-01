import {
  EllipsisIcon,
  KeyRoundIcon,
  LogOutIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCwIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
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
  canReconnect,
  liveMark,
  offersSignIn,
  serverKind,
  statusMark,
  typedLabel,
  type AccountForm,
  type Mark,
} from "~/lib/connections";
import { formatAge } from "~/lib/usageFormat";
import { cn, errorText } from "~/lib/utils";
import type { McpServer, McpServerAccount, McpServerStatus, ThreadMcp } from "~/protocol";

import { MarkChip } from "./parts";

const ACTION = "h-11 text-[13px] md:h-8 md:text-[12px]";
const FIELD = "h-11 font-mono md:h-8 md:text-[12px]";
const ITEM = "min-h-11 md:min-h-0";

/** One row: the server's own sign-in, or one of its further accounts. */
interface Member {
  name: string;
  oauth: boolean;
  status: McpServerStatus;
  error?: string;
  checkedAt?: string;
  /** The header or env names it has a value of its own for. */
  own: string[];
  /** Absent for the server's own sign-in. */
  account?: McpServerAccount;
}

function members(s: McpServer): Member[] {
  const http = serverKind(s) === "http";
  return [
    {
      name: s.name,
      oauth: s.oauth,
      status: s.status,
      error: s.error,
      checkedAt: s.checkedAt,
      own: [],
    },
    ...s.accounts.map((a) => ({
      name: a.name,
      oauth: a.oauth,
      status: a.status,
      error: a.error,
      checkedAt: a.checkedAt,
      own: http ? a.headerNames : a.envNames,
      account: a,
    })),
  ];
}

/**
 * A server's accounts. Each further one reaches agents as a server of its
 * own, `<server>-<label>`, so the agent can tell them apart, with its own
 * sign-in and, where it needs them, its own header or env values. Once
 * there are any, the server's own sign-in is the first row, so every
 * account the agents get is listed the same way.
 */
export function ServerAccounts({
  server,
  command,
  live,
  reconnecting,
  reconnectError,
  onReconnect,
  onSaved,
  onSignIn,
}: {
  server: McpServer;
  command: PageCommand;
  /** The open thread's reports on the server and its accounts. */
  live: ThreadMcp[];
  reconnecting: string | null;
  reconnectError?: { name: string; error: string };
  onReconnect: (name: string) => void;
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
  const n = server.accounts.length;
  // A command server with nothing to set differently would only run twice.
  const canAdd = http || server.envNames.length > 0;
  if (n === 0 && !canAdd) return null;

  const run = async (name: string, cmd: string, what: string) => {
    setBusy(name);
    setError("");
    try {
      const res = await command<{ server?: McpServer }>(cmd, { name });
      if (res?.server) onSaved(res.server);
    } catch (e) {
      setError(`${what} ${name} failed. ${errorText(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const example = accountName(server.name, "work");
  return (
    <section aria-label="Accounts" className="space-y-1.5">
      <DetailHeading>Accounts</DetailHeading>
      {n === 0 && (
        <p className="text-muted-foreground px-1 text-[12.5px] leading-snug">
          {http ? "Need two logins, say work and personal?" : "Need it twice with different values?"} Add an account and
          agents get it as a server of its own, like <code className="font-mono">{example}</code>.
        </p>
      )}
      {error && <ErrorLine message={error} />}
      {n > 0 && (
        <ul className="divide-y rounded-lg border">
          {members(server).map((m) =>
            m.account && editing === m.account.label ? (
              <li key={m.name} className="px-2 py-2">
                <AccountEditor
                  server={server}
                  account={m.account}
                  command={command}
                  onCancel={() => setEditing(null)}
                  onSaved={(s) => {
                    setEditing(null);
                    onSaved(s);
                  }}
                />
              </li>
            ) : (
              <MemberRow
                key={m.name}
                server={server}
                member={m}
                live={live.find((l) => l.name === m.name)}
                reconnectError={reconnectError?.name === m.name ? reconnectError.error : undefined}
                busy={busy === m.name || reconnecting === m.name}
                disabled={busy !== null || reconnecting !== null}
                onSignIn={() => onSignIn(m.name)}
                onCheck={() => void run(m.name, "check_mcp_server", "Checking")}
                onSignOut={() => void run(m.name, "sign_out_mcp_server", "Signing out")}
                onReconnect={() => onReconnect(m.name)}
                onEdit={
                  m.account &&
                  (() => {
                    setError("");
                    setEditing(m.account!.label);
                  })
                }
                onRemove={m.account && (() => setRemoving(m.account!))}
              />
            ),
          )}
        </ul>
      )}
      {editing === "" && (
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
              if (
                added &&
                offersSignIn({
                  url: s.url,
                  oauth: added.oauth,
                  status: added.status,
                })
              )
                onSignIn(name);
            }}
          />
        </div>
      )}
      {editing === null && canAdd && (
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

const CONNECTED: Mark = { label: "Connected", tone: "good" };

function MemberRow({
  server,
  member: m,
  live,
  reconnectError,
  busy,
  disabled,
  onSignIn,
  onCheck,
  onSignOut,
  onReconnect,
  onEdit,
  onRemove,
}: {
  server: McpServer;
  member: Member;
  live?: ThreadMcp;
  reconnectError?: string;
  busy: boolean;
  disabled: boolean;
  onSignIn: () => void;
  onCheck: () => void;
  onSignOut: () => void;
  onReconnect: () => void;
  onEdit?: () => void;
  onRemove?: () => void;
}) {
  const http = serverKind(server) === "http";
  const signIn = offersSignIn({
    url: server.url,
    oauth: m.oauth,
    status: m.status,
  });
  // Signed in but the running session still wants it: it started before the
  // sign-in, so it needs telling to try again.
  const reconnect = live !== undefined && canReconnect(live.status) && !signIn;
  const sub = [
    m.own.length > 0 ? `Own ${m.own.join(", ")}` : undefined,
    live ? `This thread ${liveMark(live.status).label.toLowerCase()}` : undefined,
    m.checkedAt ? `Checked ${formatAge(Date.parse(m.checkedAt), Date.now())}` : undefined,
  ].filter(Boolean);
  const problem = reconnectError
    ? `Reconnect failed. ${reconnectError}`
    : (m.error ?? (live?.status === "failed" ? live.error : undefined));
  return (
    <li className="py-0.5">
      <ListRow
        title={m.name}
        aside={m.account ? undefined : "default"}
        // The button says it already; the chip beside it is noise.
        markers={signIn ? undefined : <MarkChip mark={m.status === "connected" ? CONNECTED : statusMark(m.status)} />}
        sub={sub.length ? sub.join(" · ") : undefined}
        problem={problem}
        foldProblem
        problemTone={reconnectError || m.status === "failed" || live?.status === "failed" ? "bad" : "attention"}
        action={
          <>
            {busy && <Spinner className="text-muted-foreground size-3.5" />}
            {signIn ? (
              <Button
                size="sm"
                variant={m.status === "sign_in" ? "default" : "outline"}
                className={ACTION}
                onClick={onSignIn}
                disabled={disabled}
              >
                <KeyRoundIcon className="size-3.5" />
                Sign in
              </Button>
            ) : (
              reconnect && (
                <Button variant="outline" size="sm" className={ACTION} onClick={onReconnect} disabled={disabled}>
                  <RotateCwIcon className="size-3.5" />
                  Reconnect
                </Button>
              )
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`More for ${m.name}`}
                  className="text-muted-foreground size-11 shrink-0 md:size-8"
                  disabled={disabled}
                >
                  <EllipsisIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-40">
                {http && (
                  <DropdownMenuItem className={ITEM} onSelect={onCheck}>
                    <RefreshCwIcon />
                    Check
                  </DropdownMenuItem>
                )}
                {http && !signIn && (
                  <DropdownMenuItem className={ITEM} onSelect={onSignIn}>
                    <KeyRoundIcon />
                    Sign in again
                  </DropdownMenuItem>
                )}
                {m.oauth && (
                  <DropdownMenuItem className={ITEM} onSelect={onSignOut}>
                    <LogOutIcon />
                    Sign out
                  </DropdownMenuItem>
                )}
                {onEdit && (
                  <DropdownMenuItem className={ITEM} onSelect={onEdit}>
                    <PencilIcon />
                    Edit
                  </DropdownMenuItem>
                )}
                {onRemove && (
                  <DropdownMenuItem className={ITEM} variant="destructive" onSelect={onRemove}>
                    <Trash2Icon />
                    Remove
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />
    </li>
  );
}

/**
 * The account's name, and a value of its own for each header or env name
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
  const renamed = account !== undefined && label !== "" && label !== account.label;

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
    setForm((f) => ({
      ...f,
      values: f.values.map((v, j) => (j === i ? { ...v, ...patch } : v)),
    }));

  return (
    <form
      className="space-y-3 px-1"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <p className="text-[13px] font-medium">
        {account ? `Editing ${account.name}` : `New account for ${server.name}`}
      </p>
      <div className="space-y-1.5">
        <Label htmlFor={id}>Account name</Label>
        <Input
          id={id}
          autoFocus
          autoCapitalize="off"
          autoComplete="off"
          spellCheck={false}
          value={form.label}
          onChange={(e) => setForm((f) => ({ ...f, label: typedLabel(e.target.value) }))}
          placeholder="work"
          className={FIELD}
        />
        <p className="text-muted-foreground text-[12px] leading-snug">
          {renamed ? (
            <>
              Agents get it as <code className="font-mono">{accountName(server.name, label)}</code> from the next thread
              on. Running threads keep <code className="font-mono">{account.name}</code>.
            </>
          ) : (
            <>
              Agents get it as <code className="font-mono">{accountName(server.name, label || "work")}</code>.
            </>
          )}
        </p>
      </div>
      {form.values.length > 0 && (
        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-sm font-medium">{http ? "Headers" : "Environment"}</legend>
          {form.values.map((v, i) => (
            <div key={v.name} className="space-y-1">
              <label htmlFor={`${id}-${i}`} className="block font-mono text-[12.5px]">
                {v.name}
              </label>
              <div className="flex items-center gap-1.5">
                <Input
                  id={`${id}-${i}`}
                  type="password"
                  autoComplete="off"
                  autoCapitalize="off"
                  spellCheck={false}
                  value={v.value}
                  placeholder={v.own ? "Own value saved. Blank keeps it" : "Same as the server's"}
                  onChange={(e) => setValue(i, { value: e.target.value })}
                  className={cn(FIELD, "min-w-0 flex-1 font-sans")}
                />
                {(v.own || v.value) && (
                  <IconButton
                    label={`Use the server's ${v.name}`}
                    onClick={() => setValue(i, { value: "", own: false })}
                  >
                    <XIcon />
                  </IconButton>
                )}
              </div>
            </div>
          ))}
          <p className="text-muted-foreground text-[12px] leading-snug">
            Stored values are never shown. The cross drops the account's own value and uses the server's.
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
