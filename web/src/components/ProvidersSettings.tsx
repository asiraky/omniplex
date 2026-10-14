import { ChevronRightIcon, CircleAlertIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { useId, useMemo, useState } from "react";

import { AuthMethods } from "~/components/AuthFlowDialog";
import type { AuthWires } from "~/components/AuthFlowDialog";
import { HarnessBadge } from "~/components/HarnessBadge";
import { ModelSettingsSection } from "~/components/ModelSettingsSection";
import { SettingsPane } from "~/components/SettingsPane";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/ui/switch";
import { joinAnd } from "~/lib/format";
import { buildInstanceSpec, deriveInstanceId, hasStoredSecret, initialFieldValues } from "~/lib/providerSpec";
import { cn } from "~/lib/utils";
import type { HarnessMeta, ProviderConfigField, ProviderEnvVar, ProviderInstanceMeta } from "~/protocol";

/** A section heading, matching the weight the other settings screens use. */
function SectionHeading({ children, note }: { children: React.ReactNode; note?: string }) {
  return (
    <h3 className="text-[12px] font-medium">
      {children}
      {note && <span className="text-muted-foreground font-normal"> · {note}</span>}
    </h3>
  );
}

type View =
  | { kind: "list" }
  | { kind: "add"; harnessId: string | null }
  | { kind: "instance"; harnessId: string; instanceId: string };

/** Every instance id in play, for slug collision checks. */
function takenIds(harnesses: HarnessMeta[]): string[] {
  return harnesses.flatMap((h) => (h.instances ?? []).map((i) => i.id));
}

function ReadyBadge({ instance }: { instance: ProviderInstanceMeta }) {
  if (instance.enabled === false) {
    return (
      <Badge variant="outline" className="text-muted-foreground text-[10px]">
        disabled
      </Badge>
    );
  }
  if (instance.availability.state === "ready") {
    return (
      <Badge variant="outline" className="text-[10px] text-green-600 dark:text-green-500">
        ready
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className="text-attention-foreground text-[10px]">
      {instance.availability.reason || "unavailable"}
    </Badge>
  );
}

/**
 * Who an account is signed in as. The probe reports the email where the
 * harness tells it; an account whose fix is signing in is plainly signed out,
 * unless the harness already said so in its own words, which the badge shows.
 * Anything else (an API key, a harness that does not say) has nothing to name.
 */
function signedIn(instance: ProviderInstanceMeta): { label: string; out: boolean } | null {
  const account = instance.availability.facts?.account;
  if (account) return { label: account, out: false };
  if (
    !instance.availability.reason &&
    instance.availability.remedy?.some((r) => r.action === "login")
  ) {
    return { label: "Not signed in", out: true };
  }
  return null;
}

/** The signed-in line of an account's own screen. */
function SignedInLine({ instance }: { instance: ProviderInstanceMeta }) {
  const who = signedIn(instance);
  if (!who) return null;
  return (
    <p className={cn("text-[12px] break-all", who.out && "text-attention-foreground")}>
      {who.out ? (
        who.label
      ) : (
        <>
          <span className="text-muted-foreground">Signed in as </span>
          <span className="font-medium">{who.label}</span>
        </>
      )}
    </p>
  );
}

/** A compact warning line, the same shape the Usage page's stale note has. */
function Warning({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="text-attention-foreground bg-attention-surface/70 flex items-start gap-1.5 rounded-md px-2 py-1.5 text-[11px] leading-relaxed"
    >
      <CircleAlertIcon aria-hidden className="mt-0.5 size-3 shrink-0" />
      <div className="min-w-0 flex-1 space-y-1.5">{children}</div>
    </div>
  );
}

/**
 * What is wrong with an account's wiring: another account signed in to the
 * same login (so the two share one set of limits), and shared config the
 * account has its own copy of. Each copy can be swapped for the shared entry
 * here; the server moves the copy aside rather than deleting it, then pushes
 * a fresh harnesses frame without the conflict.
 */
function AccountWarnings({
  instance,
  harness,
  wires,
  onError,
}: {
  instance: ProviderInstanceMeta;
  harness: HarnessMeta;
  wires: AuthWires;
  onError: (message: string | null) => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const same = instance.sameLoginAs ?? [];
  const conflicts = instance.homeConflicts ?? [];
  if (same.length === 0 && conflicts.length === 0) return null;

  const names = same.map(
    (id) => (harness.instances ?? []).find((i) => i.id === id)?.displayName || id,
  );

  const share = async (entry: string) => {
    setBusy(entry);
    onError(null);
    try {
      await wires.command("use_shared_home_entry", { instanceId: instance.id, entry });
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-1.5">
      {same.length > 0 && (
        <Warning>
          <p>
            Signed in to the same login as {joinAnd(names)}, so they share one set of limits.
            Sign one of them in to a different login.
          </p>
        </Warning>
      )}
      {conflicts.length > 0 && (
        <Warning>
          <p>
            Has its own copy of {joinAnd(conflicts.map((c) => c.entry))} instead of the shared
            config, so changes to the shared one do not reach it.
          </p>
          <ul className="space-y-1">
            {conflicts.map((c) => (
              <li key={c.entry} className="flex flex-wrap items-center gap-2">
                <span className="text-foreground min-w-0 flex-1 font-mono break-all">{c.entry}</span>
                <Button
                  variant="outline"
                  size="sm"
                  className="max-md:h-9"
                  aria-label={`Use shared ${c.entry}`}
                  disabled={busy !== null}
                  onClick={() => void share(c.entry)}
                >
                  {busy === c.entry && <Spinner aria-hidden className="size-3.5" />}
                  Use shared
                </Button>
              </li>
            ))}
          </ul>
          <p className="text-muted-foreground">
            Use shared keeps the account's copy beside it as a .account-backup and links the
            shared one in its place.
          </p>
        </Warning>
      )}
    </div>
  );
}

/**
 * The schema-driven config fields for one driver. Plain fields are prefilled
 * from the env the server echoed, so blank means the user cleared it. Secrets
 * are never echoed and never prefilled: a stored one shows as the
 * "(unchanged)" placeholder, and leaving it blank keeps it.
 */
function ConfigFields({
  fields,
  values,
  onChange,
  stored,
}: {
  fields: ProviderConfigField[];
  values: Record<string, string>;
  onChange: (env: string, value: string) => void;
  /** The instance's echoed env (meta.env); absent for a brand-new instance. */
  stored?: ProviderEnvVar[];
}) {
  const idBase = useId();
  return (
    <>
      {fields.map((f) => (
        <div key={f.env} className="space-y-1.5">
          <Label htmlFor={`${idBase}-${f.env}`}>{f.label}</Label>
          <Input
            id={`${idBase}-${f.env}`}
            // Secrets are masked on screen; their values exist only in this
            // input and in the spec frame that carries them, nowhere else.
            type={f.kind === "secret" ? "password" : "text"}
            autoComplete="off"
            value={values[f.env] ?? ""}
            onChange={(e) => onChange(f.env, e.target.value)}
            placeholder={
              f.kind === "secret" && hasStoredSecret(stored, f.env) ? "(unchanged)" : f.placeholder
            }
            className={f.kind === "secret" ? "md:text-[12px]" : "font-mono md:text-[12px]"}
          />
          {f.description && <p className="text-muted-foreground text-[11px]">{f.description}</p>}
        </div>
      ))}
    </>
  );
}

/** The list view: instances grouped under their harness. */
function InstanceList({
  harnesses,
  onOpen,
  onAdd,
  wires,
  onError,
}: {
  harnesses: HarnessMeta[];
  onOpen: (harnessId: string, instanceId: string) => void;
  /** Start the add wizard for one driver. */
  onAdd: (harnessId: string) => void;
  wires: AuthWires;
  onError: (message: string | null) => void;
}) {
  return (
    <div className="space-y-5">
      {harnesses.map((h) => (
        <div key={h.id} className="space-y-2">
          <div className="flex items-center gap-2">
            <HarnessBadge harness={h.id} accent={h.accent} className="size-4" />
            <SectionHeading>{h.name}</SectionHeading>
          </div>
          <div className="overflow-hidden rounded-lg border">
            {(h.instances ?? []).map((inst, i) => {
              const who = signedIn(inst);
              return (
                <div key={inst.id} className={cn(i > 0 && "border-t")}>
                  <button
                    type="button"
                    onClick={() => onOpen(h.id, inst.id)}
                    className="hover:bg-accent flex w-full items-center gap-2 px-3 py-2.5 text-left"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="flex flex-wrap items-center gap-1.5 text-[12px] font-medium">
                        {inst.displayName || inst.id}
                        <ReadyBadge instance={inst} />
                      </span>
                      {who && (
                        <span
                          className={cn(
                            "block text-[12px] break-all",
                            who.out && "text-attention-foreground",
                          )}
                        >
                          {who.label}
                        </span>
                      )}
                      <span className="text-muted-foreground block truncate text-[11px]">
                        {[
                          inst.availability.facts?.auth,
                          inst.models?.length
                            ? `${inst.models.length} model${inst.models.length === 1 ? "" : "s"}`
                            : null,
                        ]
                          .filter(Boolean)
                          .join(" · ") || inst.id}
                      </span>
                    </span>
                    <ChevronRightIcon aria-hidden className="text-muted-foreground size-4 shrink-0" />
                  </button>
                  {!!(inst.sameLoginAs?.length || inst.homeConflicts?.length) && (
                    <div className="px-3 pb-2.5">
                      <AccountWarnings instance={inst} harness={h} wires={wires} onError={onError} />
                    </div>
                  )}
                </div>
              );
            })}
            {(h.instances ?? []).length === 0 && (
              <div className="flex flex-wrap items-center gap-2 px-3 py-2.5">
                <p className="text-muted-foreground min-w-0 flex-1 text-[12px]">
                  No {h.name} account yet.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="max-md:h-9"
                  aria-label={`Add ${h.name} account`}
                  onClick={() => onAdd(h.id)}
                >
                  <PlusIcon />
                  Add account
                </Button>
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

/**
 * The add wizard: pick a harness, name the account, fill its fields. The id is
 * derived from the name and fixed at creation — it is the routing key threads
 * record, so it must not drift when the account is later renamed.
 */
function AddInstance({
  harnesses,
  view,
  setView,
  wires,
  onError,
}: {
  harnesses: HarnessMeta[];
  view: Extract<View, { kind: "add" }>;
  setView: (v: View) => void;
  wires: AuthWires;
  onError: (message: string | null) => void;
}) {
  const [name, setName] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const harness = harnesses.find((h) => h.id === view.harnessId) ?? null;
  const id = useMemo(
    () => (harness ? deriveInstanceId(name || harness.name, takenIds(harnesses)) : ""),
    [name, harness, harnesses],
  );

  if (!harness) {
    return (
      <div className="space-y-2">
        <SectionHeading>Which agent?</SectionHeading>
        <div className="overflow-hidden rounded-lg border">
          {harnesses.map((h, i) => (
            <button
              key={h.id}
              type="button"
              onClick={() => setView({ kind: "add", harnessId: h.id })}
              className={
                "hover:bg-accent flex w-full items-center gap-2 px-3 py-2.5 text-left" +
                (i > 0 ? " border-t" : "")
              }
            >
              <HarnessBadge harness={h.id} accent={h.accent} className="size-4" />
              <span className="flex-1 text-[12px] font-medium">{h.name}</span>
              <ChevronRightIcon aria-hidden className="text-muted-foreground size-4" />
            </button>
          ))}
        </div>
      </div>
    );
  }

  const add = async (connect: boolean) => {
    setBusy(true);
    onError(null);
    try {
      const spec = buildInstanceSpec({
        id,
        driver: harness.id,
        displayName: name.trim() || harness.name,
        enabled: true,
        fields: harness.configFields ?? [],
        values,
      });
      await wires.command("add_provider_instance", { spec });
      // Land on the new account — its auth section, when "and connect" —
      // rather than dumping back at the list.
      setView(connect ? { kind: "instance", harnessId: harness.id, instanceId: id } : { kind: "list" });
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2">
        <HarnessBadge harness={harness.id} accent={harness.accent} className="size-4" />
        <SectionHeading>New {harness.name} account</SectionHeading>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="add-instance-name">Name</Label>
        <Input
          id="add-instance-name"
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Work, Personal…"
        />
        <p className="text-muted-foreground text-[11px]">
          id <span className="font-mono">{id}</span> — fixed once created
        </p>
      </div>
      <ConfigFields
        fields={harness.configFields ?? []}
        values={values}
        onChange={(env, v) => setValues((s) => ({ ...s, [env]: v }))}
      />
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="ghost" onClick={() => setView({ kind: "list" })} disabled={busy}>
          Cancel
        </Button>
        <Button variant="outline" onClick={() => void add(false)} disabled={busy}>
          Add
        </Button>
        <Button onClick={() => void add(true)} disabled={busy}>
          {busy ? "Adding…" : "Add and connect"}
        </Button>
      </div>
    </div>
  );
}

/** One instance's screen: edit form, sign-in methods, and removal. */
function InstanceView({
  harnesses,
  view,
  setView,
  wires,
  onOpenTerminal,
  onError,
}: {
  harnesses: HarnessMeta[];
  view: Extract<View, { kind: "instance" }>;
  setView: (v: View) => void;
  wires: AuthWires;
  onOpenTerminal: (instanceId: string) => void;
  onError: (message: string | null) => void;
}) {
  const harness = harnesses.find((h) => h.id === view.harnessId);
  const instance = harness?.instances?.find((i) => i.id === view.instanceId);
  const [name, setName] = useState(instance?.displayName ?? view.instanceId);
  // Starts from what is stored (the server echoes plain values; secrets stay
  // blank behind their placeholder), so a save carries the truth, not blanks.
  const [values, setValues] = useState<Record<string, string>>(() =>
    initialFieldValues(harness?.configFields ?? [], instance?.env),
  );
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);

  if (!harness || !instance) {
    // Removed under us by another device; the pushed harnesses frame already
    // took it out of the list this screen returns to.
    return (
      <div className="space-y-3">
        <p className="text-muted-foreground text-[12px]">This account no longer exists.</p>
        <Button variant="outline" size="sm" onClick={() => setView({ kind: "list" })}>
          Back to list
        </Button>
      </div>
    );
  }

  const fields = harness.configFields ?? [];
  const knownDriver = harnesses.some((h) => h.id === instance.driver);

  const save = async (enabled: boolean) => {
    setBusy(true);
    onError(null);
    try {
      // Merged over the echoed env, so a rename or a toggle carries every
      // stored var through — including ones this schema has no field for.
      const spec = buildInstanceSpec({
        id: instance.id,
        driver: instance.driver,
        displayName: name,
        enabled,
        fields,
        values,
        stored: instance.env,
      });
      await wires.command("save_provider_instance", { spec });
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    onError(null);
    try {
      await wires.command("delete_provider_instance", { instanceId: instance.id });
      setView({ kind: "list" });
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e));
    } finally {
      // On success this view is already on its way out, so the reset only
      // matters for a failed delete; here it runs either way.
      setBusy(false);
      setConfirming(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <HarnessBadge harness={instance.driver} accent={harness.accent} className="size-4" />
        <SectionHeading>{instance.displayName || instance.id}</SectionHeading>
        <ReadyBadge instance={instance} />
        <span className="text-muted-foreground ml-auto font-mono text-[10px]">{instance.id}</span>
      </div>

      <SignedInLine instance={instance} />

      <AccountWarnings instance={instance} harness={harness} wires={wires} onError={onError} />

      {!knownDriver && (
        <Alert>
          <AlertDescription className="text-[12px]">
            This server has no driver called “{instance.driver}”, so the account cannot run. It is
            kept here so it can be removed or carried to a build that has one.
          </AlertDescription>
        </Alert>
      )}

      {instance.availability.state === "unavailable" && instance.availability.reason && (
        <p className="text-attention-foreground text-[12px]">{instance.availability.reason}</p>
      )}

      <div className="flex items-center justify-between gap-2">
        <div>
          <SectionHeading>Enabled</SectionHeading>
          <p className="text-muted-foreground text-[11px]">
            A disabled account keeps its settings but offers no models.
          </p>
        </div>
        <Switch
          aria-label="Enabled"
          checked={instance.enabled !== false}
          disabled={busy}
          onCheckedChange={(on) => void save(on)}
        />
      </div>

      <Separator />

      <div className="space-y-3">
        <SectionHeading>Settings</SectionHeading>
        <div className="space-y-1.5">
          <Label htmlFor="instance-name">Display name</Label>
          <Input id="instance-name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <ConfigFields
          fields={fields}
          values={values}
          onChange={(env, v) => setValues((s) => ({ ...s, [env]: v }))}
          stored={instance.env}
        />
        {fields.some((f) => f.kind === "secret" && hasStoredSecret(instance.env, f.env)) && (
          <p className="text-muted-foreground text-[11px]">
            Stored secrets are never shown. Leave one blank to keep it; type to replace it.
          </p>
        )}
        <div className="flex justify-end">
          <Button size="sm" disabled={busy} onClick={() => void save(instance.enabled !== false)}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>

      {harness.modelSettings && (
        <>
          <Separator />
          <ModelSettingsSection
            wires={wires}
            instance={instance}
            schema={harness.modelSettings}
          />
        </>
      )}

      <Separator />

      <div className="space-y-2">
        <SectionHeading>Sign-in</SectionHeading>
        {instance.auth ? (
          <AuthMethods
            wires={wires}
            instanceId={instance.id}
            onOpenTerminal={() => onOpenTerminal(instance.id)}
          />
        ) : (
          <p className="text-muted-foreground text-[12px]">
            This account has no sign-in of its own.
          </p>
        )}
      </div>

      <Separator />

      <div className="space-y-2">
        <SectionHeading note="cannot be undone">Remove account</SectionHeading>
        <p className="text-muted-foreground text-[11px]">
          Takes the account out of the config. Threads that used it keep their transcripts.
        </p>
        {confirming ? (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[12px]">Remove “{instance.displayName || instance.id}”?</span>
            <div className="ml-auto flex gap-2">
              <Button variant="ghost" size="sm" onClick={() => setConfirming(false)} disabled={busy}>
                Cancel
              </Button>
              <Button variant="destructive" size="sm" onClick={() => void remove()} disabled={busy}>
                {busy ? (
                  <>
                    <Spinner aria-hidden className="size-4" />
                    Removing…
                  </>
                ) : (
                  "Remove"
                )}
              </Button>
            </div>
          </div>
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setConfirming(true)}
            className="text-destructive hover:text-destructive"
          >
            <Trash2Icon />
            Remove account
          </Button>
        )}
      </div>
    </div>
  );
}

/**
 * The providers screen: every account of every agent, and the only place they
 * are added, edited, connected and removed. Renders live off the harnesses
 * prop — the server pushes a fresh harnesses frame after every change, so
 * there is no local copy of the list to fall out of date.
 */
export default function ProvidersSettings({
  harnesses,
  wires,
  onOpenTerminal,
  onRecheck,
  onBack,
  startAdding,
}: {
  harnesses: HarnessMeta[];
  wires: AuthWires;
  /** Open the embedded login terminal for an instance (terminal-only auth). */
  onOpenTerminal: (instanceId: string) => void;
  /** Re-probe the harnesses; the refreshed list arrives via the prop. */
  onRecheck: () => Promise<void> | void;
  onBack?: () => void;
  /** Open on the add wizard, for one driver or (null) the driver picker. */
  startAdding?: { harnessId: string | null };
}) {
  const [view, setView] = useState<View>(() =>
    startAdding ? { kind: "add", harnessId: startAdding.harnessId } : { kind: "list" },
  );
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);

  const go = (v: View) => {
    setError(null);
    setView(v);
  };

  const recheck = async () => {
    setChecking(true);
    try {
      await onRecheck();
    } catch {
      // A failed recheck leaves the current picture standing; nothing to say.
    } finally {
      setChecking(false);
    }
  };

  return (
    <SettingsPane
      title="Providers"
      description="The agents this server can run and the accounts they sign in with."
      onBack={view.kind !== "list" ? () => go({ kind: "list" }) : onBack}
      error={error}
    >
      {view.kind === "list" && (
        <>
          <InstanceList
            harnesses={harnesses}
            onOpen={(harnessId, instanceId) => go({ kind: "instance", harnessId, instanceId })}
            onAdd={(harnessId) => go({ kind: "add", harnessId })}
            wires={wires}
            onError={setError}
          />
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={() => go({ kind: "add", harnessId: null })}>
              <PlusIcon />
              Add account
            </Button>
            <Button variant="outline" size="sm" disabled={checking} onClick={() => void recheck()}>
              {checking ? <Spinner aria-hidden className="size-3.5" /> : <RefreshCwIcon />}
              Check again
            </Button>
          </div>
        </>
      )}
      {view.kind === "add" && (
        <AddInstance
          harnesses={harnesses}
          view={view}
          setView={go}
          wires={wires}
          onError={setError}
        />
      )}
      {view.kind === "instance" && (
        <InstanceView
          // Keyed so moving between accounts resets the form's local
          // state instead of carrying one account's edits to another.
          key={view.instanceId}
          harnesses={harnesses}
          view={view}
          setView={go}
          wires={wires}
          onOpenTerminal={onOpenTerminal}
          onError={setError}
        />
      )}
    </SettingsPane>
  );
}
