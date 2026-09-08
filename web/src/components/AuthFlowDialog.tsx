import { CheckIcon, CopyIcon, ExternalLinkIcon, KeyRoundIcon, TerminalIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import { answeredPrompt, applyAuthFlowEvent, emptyAuthFlowView } from "~/lib/authFlow";
import type { AuthFlowView } from "~/lib/authFlow";
import { useCopy } from "~/lib/clipboard";
import type { AuthFlowEvent, AuthFlowNotice, AuthMethod, AuthStatus, InstanceAuth } from "~/protocol";

/** The two client capabilities every piece of this surface needs. */
export interface AuthWires {
  command: (command: string, args: unknown) => Promise<any>;
  /** Client.onAuthFlow — events for one flow, replayed if they raced the ack. */
  subscribe: (flowId: string, listener: (ev: AuthFlowEvent) => void) => () => void;
}

/**
 * The embedded-terminal fallback is a method like any other in the overview,
 * but it must never be auth_begin'd — its whole point is that the sign-in is a
 * CLI, run in the existing login terminal instead.
 */
const TERMINAL_METHOD = "terminal";

/** One line of flow narration. The url and device-code shapes get real UI —
    an open button and a code you can actually read across the room — because
    they are the moment the user leaves this window and must carry something. */
function Notice({ notice }: { notice: AuthFlowNotice }) {
  const { copied, copy } = useCopy();
  switch (notice.type) {
    case "auth_url":
      return (
        <div className="space-y-2">
          {notice.message && <p className="text-[12px]">{notice.message}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <Button asChild size="sm">
              <a href={notice.url} target="_blank" rel="noreferrer">
                <ExternalLinkIcon />
                Open sign-in page
              </a>
            </Button>
            {/* The device this UI is on may not be where the browser session
                lives; the raw URL travels by copy for that case. */}
            <Button variant="outline" size="sm" onClick={() => void copy(notice.url ?? "")}>
              {copied ? <CheckIcon /> : <CopyIcon />}
              Copy URL
            </Button>
          </div>
          {notice.url && (
            <p className="text-muted-foreground font-mono text-[10px] break-all">{notice.url}</p>
          )}
        </div>
      );
    case "device_code":
      return (
        <div className="space-y-2">
          <p className="text-[12px]">
            {notice.instructions ?? notice.message ?? "Enter this code on the verification page."}
          </p>
          {notice.userCode && (
            <button
              type="button"
              onClick={() => void copy(notice.userCode ?? "")}
              className="hover:bg-accent block w-full rounded-lg border px-4 py-3 text-center font-mono text-xl tracking-[0.3em]"
              aria-label="Copy code"
            >
              {notice.userCode}
            </button>
          )}
          {notice.verificationUri && (
            <Button asChild size="sm" variant="outline">
              <a href={notice.verificationUri} target="_blank" rel="noreferrer">
                <ExternalLinkIcon />
                {notice.verificationUri.replace(/^https?:\/\//, "")}
              </a>
            </Button>
          )}
        </div>
      );
    case "progress":
      return (
        <p className="text-muted-foreground flex items-center gap-2 text-[12px]">
          <Spinner aria-hidden className="size-3.5" />
          {notice.message}
        </p>
      );
    default:
      return <p className="text-[12px]">{notice.message}</p>;
  }
}

/** The question the flow is waiting on. Secrets are masked and travel only in
    the auth_respond frame — never into logs, state, or error strings. */
function PromptField({
  prompt,
  onAnswer,
}: {
  prompt: NonNullable<AuthFlowView["prompt"]>;
  onAnswer: (value: string) => void;
}) {
  const [value, setValue] = useState("");
  if (prompt.options?.length) {
    return (
      <div className="space-y-2">
        <p className="text-[12px]">{prompt.message}</p>
        <div className="flex flex-wrap gap-2">
          {prompt.options.map((o) => (
            <Button key={o.id} variant="outline" size="sm" onClick={() => onAnswer(o.id)}>
              {o.label}
            </Button>
          ))}
        </div>
      </div>
    );
  }
  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (value) onAnswer(value);
      }}
    >
      <label className="block text-[12px]" htmlFor="auth-flow-answer">
        {prompt.message}
      </label>
      <div className="flex gap-2">
        <Input
          id="auth-flow-answer"
          autoFocus
          type={prompt.secret ? "password" : "text"}
          autoComplete="off"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={prompt.placeholder}
          className="min-w-0 flex-1 font-mono md:text-[12px]"
        />
        <Button type="submit" disabled={!value}>
          Submit
        </Button>
      </div>
    </form>
  );
}

/**
 * One running sign-in flow: begins it on mount, renders the narration as it
 * arrives, relays answers, and cancels the flow if the user walks away before
 * it finishes. Lives entirely in local state — a flow is bound to this
 * connection and this dialog, and nothing about it may be persisted.
 */
export function AuthFlowRun({
  wires,
  instanceId,
  methodId,
  onFinished,
  onClose,
}: {
  wires: AuthWires;
  instanceId: string;
  methodId: string;
  /** Called on successful completion, after the user has seen it succeed. */
  onFinished: () => void;
  onClose: () => void;
}) {
  const [view, setView] = useState<AuthFlowView>(emptyAuthFlowView);
  const [flowId, setFlowId] = useState<string | null>(null);
  // Read by the unmount cleanup, which must not cancel a finished flow.
  const doneRef = useRef(false);
  doneRef.current = view.done;

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;
    let startedFlow: string | null = null;
    wires
      .command("auth_begin", { instanceId, methodId })
      .then((result: { flowId?: string }) => {
        const id = result?.flowId;
        if (!id) throw new Error("the server did not return a flow id");
        if (cancelled) {
          // The dialog closed before the ack landed; the flow still started.
          void wires.command("auth_cancel", { flowId: id }).catch(() => {});
          return;
        }
        startedFlow = id;
        setFlowId(id);
        unsubscribe = wires.subscribe(id, (ev) => setView((v) => applyAuthFlowEvent(v, ev)));
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setView((v) =>
          applyAuthFlowEvent(v, {
            flowId: "",
            error: e instanceof Error ? e.message : String(e),
          }),
        );
      });
    return () => {
      cancelled = true;
      unsubscribe?.();
      if (startedFlow && !doneRef.current) {
        void wires.command("auth_cancel", { flowId: startedFlow }).catch(() => {});
      }
    };
    // A flow runs once per (instance, method) mount; changing either remounts.
  }, [wires, instanceId, methodId]);

  const answer = (value: string) => {
    if (!flowId || !view.prompt) return;
    const promptId = view.prompt.id;
    setView(answeredPrompt);
    wires.command("auth_respond", { flowId, promptId, value }).catch((e: unknown) => {
      setView((v) =>
        applyAuthFlowEvent(v, { flowId, error: e instanceof Error ? e.message : String(e) }),
      );
    });
  };

  const succeeded = view.done && !view.error;

  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {view.notices.length === 0 && !view.done && (
          <p className="text-muted-foreground flex items-center gap-2 text-[12px]">
            <Spinner aria-hidden className="size-3.5" />
            Starting sign-in…
          </p>
        )}
        {view.notices.map((n, i) => (
          <Notice key={i} notice={n} />
        ))}
      </div>

      {view.prompt && <PromptField key={view.prompt.id} prompt={view.prompt} onAnswer={answer} />}

      {view.error && (
        <Alert variant="destructive">
          <AlertDescription className="text-[12px] break-words">{view.error}</AlertDescription>
        </Alert>
      )}

      {succeeded && (
        <p className="flex items-center gap-2 text-[12px]">
          <CheckIcon aria-hidden className="size-4 text-green-600 dark:text-green-500" />
          Signed in.
        </p>
      )}

      <div className="flex justify-end gap-2">
        {succeeded ? (
          <Button onClick={onFinished}>Done</Button>
        ) : (
          <Button variant="ghost" onClick={onClose}>
            {view.error ? "Close" : "Cancel"}
          </Button>
        )}
      </div>
    </div>
  );
}

function statusFor(auth: InstanceAuth, methodId: string): AuthStatus | undefined {
  return auth.statuses?.find((s) => s.methodId === methodId);
}

/**
 * The instance's sign-in methods with their live credential state, and the
 * buttons that act on them. Fetching the overview asks the harness about its
 * credentials, which can genuinely take seconds — hence the honest loading row
 * rather than an empty flash.
 */
export function AuthMethods({
  wires,
  instanceId,
  onOpenTerminal,
  onChanged,
}: {
  wires: AuthWires;
  instanceId: string;
  /** Open the embedded login terminal for this instance. */
  onOpenTerminal: () => void;
  /** A credential changed (flow finished, disconnect) — refetch whatever cares. */
  onChanged?: () => void;
}) {
  const [auth, setAuth] = useState<InstanceAuth | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [running, setRunning] = useState<string | null>(null); // methodId of the open flow
  const [busy, setBusy] = useState<string | null>(null); // methodId of an in-flight disconnect

  const load = useCallback(() => {
    setError(null);
    wires
      .command("provider_auth_overview", { instanceId })
      .then((r: { auth?: InstanceAuth }) => setAuth(r?.auth ?? { methods: [] }))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [wires, instanceId]);

  useEffect(() => {
    setAuth(null);
    load();
  }, [load]);

  const disconnect = async (methodId: string) => {
    setBusy(methodId);
    setError(null);
    try {
      await wires.command("logout_provider", { instanceId, methodId });
      load();
      onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  if (error && !auth) {
    return (
      <Alert variant="destructive">
        <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
      </Alert>
    );
  }
  if (!auth) {
    return (
      <p className="text-muted-foreground flex items-center gap-2 text-[12px]">
        <Spinner aria-hidden className="size-3.5" />
        Checking credentials…
      </p>
    );
  }
  if (auth.methods.length === 0) {
    return <p className="text-muted-foreground text-[12px]">No sign-in needed.</p>;
  }

  return (
    <div className="space-y-3">
      {auth.methods.map((m: AuthMethod) => {
        const status = statusFor(auth, m.id);
        const connected = status?.state === "connected";
        if (running === m.id) {
          return (
            <div key={m.id} className="rounded-lg border p-3">
              <p className="mb-3 text-[12px] font-medium">{m.label}</p>
              <AuthFlowRun
                wires={wires}
                instanceId={instanceId}
                methodId={m.id}
                onFinished={() => {
                  setRunning(null);
                  load();
                  onChanged?.();
                }}
                onClose={() => setRunning(null)}
              />
            </div>
          );
        }
        return (
          <div key={m.id} className="flex flex-wrap items-center gap-2 rounded-lg border p-3">
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="flex flex-wrap items-center gap-1.5 text-[12px] font-medium">
                {m.label}
                {m.subscription && (
                  <Badge variant="secondary" className="text-[10px]">
                    subscription
                  </Badge>
                )}
                {connected ? (
                  <Badge variant="outline" className="text-[10px] text-green-600 dark:text-green-500">
                    connected
                  </Badge>
                ) : (
                  <Badge variant="outline" className="text-muted-foreground text-[10px]">
                    not connected
                  </Badge>
                )}
              </p>
              {(status?.account || m.description) && (
                <p className="text-muted-foreground text-[11px]">
                  {status?.account ?? m.description}
                  {/* Where the credential lives matters when disconnecting: an
                      environment credential cannot be removed from here. */}
                  {status?.source === "environment" && " · from environment"}
                </p>
              )}
              {status?.detail && <p className="text-muted-foreground text-[11px]">{status.detail}</p>}
            </div>
            <div className="flex shrink-0 gap-2">
              {m.kind === "terminal" || m.id === TERMINAL_METHOD ? (
                <Button variant="outline" size="sm" onClick={onOpenTerminal}>
                  <TerminalIcon />
                  Open terminal
                </Button>
              ) : (
                <Button
                  variant={connected ? "outline" : "default"}
                  size="sm"
                  onClick={() => setRunning(m.id)}
                >
                  <KeyRoundIcon />
                  {connected ? "Reconnect" : "Connect"}
                </Button>
              )}
              {/* Terminal-only methods have no structured logout — the harness
                  owns the credential and the server would refuse the request. */}
              {connected && m.kind !== "terminal" && m.id !== TERMINAL_METHOD && (
                <Button
                  variant="outline"
                  size="sm"
                  className="text-destructive hover:text-destructive"
                  disabled={busy === m.id}
                  onClick={() => void disconnect(m.id)}
                >
                  {busy === m.id ? <Spinner aria-hidden className="size-3.5" /> : "Disconnect"}
                </Button>
              )}
            </div>
          </div>
        );
      })}
      {error && (
        <Alert variant="destructive">
          <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

/**
 * The standalone sign-in dialog — what the header key icon and the in-session
 * recovery card open for a flows-capable instance, so signing back in does not
 * require finding the instance in the providers screen first.
 */
export default function InstanceAuthDialog({
  wires,
  instanceId,
  instanceName,
  onOpenTerminal,
  onClose,
}: {
  wires: AuthWires;
  instanceId: string;
  instanceName: string;
  onOpenTerminal: () => void;
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent fullscreenOnMobile className="flex max-h-[min(90dvh,40rem)] flex-col gap-0 p-0 md:max-w-md">
        <DialogHeader className="border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>Sign in — {instanceName}</DialogTitle>
          <DialogDescription>
            Connect a credential for this account. Sessions pick the change up as soon as it lands.
          </DialogDescription>
        </DialogHeader>
        <div className="scroll-thin min-h-0 flex-1 overflow-y-auto px-6 py-5">
          <AuthMethods wires={wires} instanceId={instanceId} onOpenTerminal={onOpenTerminal} />
        </div>
      </DialogContent>
    </Dialog>
  );
}
