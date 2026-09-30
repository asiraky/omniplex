import { ArrowRightLeftIcon, LogInIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { Button } from "~/components/ui/button";
import type { Turn } from "~/protocol";

type SwitchTarget = { id: string; name: string };

type InterruptedProps = {
  turn: Turn;
  onContinue: () => void;
  onLogin?: () => void;
  providerName?: string;
  /** Live: true once the instance reports ready again after a sign-in. */
  providerReady?: boolean;
  /** Re-sends this turn's prompt. Only ever called by the Retry button —
      finishing a sign-in must never resend a prompt by itself. */
  onRetryTurn?: (turn: Turn) => void;
  /** The harness's other ready accounts, offered when this one hit a limit. */
  switchTargets?: SwitchTarget[];
  /** The account the thread was moved to since this turn failed, if it was. */
  switchedTo?: string;
  /** Moves the thread to another account, then re-sends this turn.
      Resolves false when the switch did not happen, so the card can be used again. */
  onSwitchAccount?: (instance: string, turn: Turn) => Promise<boolean>;
};

// Whether a button on the card has already sent something, and the setter the
// buttons lock it with. Held by InterruptedCard so every variant shares it.
type Sending = { sending: boolean; setSending: (v: boolean) => void };

function FailureBox({ children }: { children: ReactNode }) {
  return (
    <div className="fade-in border-destructive/30 bg-destructive/5 rounded-lg border px-3.5 py-3">
      {children}
    </div>
  );
}

function SwitchAccountButton({
  target,
  primary,
  turn,
  onSwitchAccount,
  sending,
  setSending,
}: Sending & {
  target: SwitchTarget;
  primary: boolean;
  turn: Turn;
  onSwitchAccount: (instance: string, turn: Turn) => Promise<boolean>;
}) {
  return (
    <Button
      size="sm"
      variant={primary ? "default" : "outline"}
      disabled={sending}
      onClick={async () => {
        setSending(true);
        let switched = false;
        try {
          switched = await onSwitchAccount(target.id, turn);
        } finally {
          // Refused or failed alike, the card has to be usable
          // again. A switch that went through keeps it locked: the
          // retry it triggers is already on its way.
          // react-doctor-disable-next-line react-doctor/no-loading-flag-reset-outside-finally -- the reset is in finally; it skips only the success path, which must stay locked
          if (!switched) setSending(false);
        }
      }}
    >
      <ArrowRightLeftIcon />
      Continue on {target.name}
    </Button>
  );
}

// A usage limit is not fixed by trying again on the same account; it is
// fixed by waiting for the reset the error names, or by carrying the
// conversation to another account and running the prompt there.
function LimitCard({
  turn,
  providerName,
  onRetryTurn,
  switchTargets,
  switchedTo,
  onSwitchAccount,
  sending,
  setSending,
}: Sending &
  Pick<
    InterruptedProps,
    "turn" | "providerName" | "onRetryTurn" | "switchedTo" | "onSwitchAccount"
  > & {
    switchTargets: SwitchTarget[];
  }) {
  const retry = onRetryTurn && (
    <Button
      size="sm"
      variant={switchedTo ? "default" : "outline"}
      disabled={sending}
      onClick={() => {
        setSending(true);
        onRetryTurn(turn);
      }}
    >
      {sending ? "Sending…" : "Retry this prompt"}
    </Button>
  );
  return (
    <FailureBox>
      {/* No error text: the harness's own message just above already says
          when the limit resets. */}
      <p className="text-[13px]">{providerName ?? "This account"} hit its usage limit.</p>
      {switchedTo ? (
        <>
          <p className="text-muted-foreground mt-1.5 text-[12px]">
            The thread is on {switchedTo} now. Retry to run the prompt there.
          </p>
          <div className="mt-2.5 flex flex-wrap gap-2">{retry}</div>
        </>
      ) : (
        <>
          <p className="text-muted-foreground mt-1.5 text-[12px]">
            {switchTargets.length && onSwitchAccount
              ? "Continue on another account: the conversation moves with the thread, and this prompt runs there."
              : "Wait for the reset, or sign another account in to this harness to continue on it."}
          </p>
          <div className="mt-2.5 flex flex-wrap gap-2">
            {onSwitchAccount &&
              switchTargets.map((t, i) => (
                <SwitchAccountButton
                  key={t.id}
                  target={t}
                  primary={i === 0}
                  turn={turn}
                  onSwitchAccount={onSwitchAccount}
                  sending={sending}
                  setSending={setSending}
                />
              ))}
            {retry}
          </div>
        </>
      )}
    </FailureBox>
  );
}

// A turn that died for want of a login is not an interruption, and
// continuing it cannot work: the next attempt fails the same way. What it
// needs is the one instruction that fixes it.
function LoginCard({
  turn,
  onLogin,
  providerName,
  providerReady,
  onRetryTurn,
  sending,
  setSending,
}: Sending &
  Pick<InterruptedProps, "turn" | "onLogin" | "providerName" | "providerReady" | "onRetryTurn">) {
  return (
    <FailureBox>
      <p className="text-[13px]">
        {providerName ?? "The provider"} needs to sign in again, so this turn could not run.
      </p>
      <p className="text-muted-foreground mt-1.5 text-[12px]">
        The thread and its prompt are safe. Sign in, then retry — the prompt is only ever re-sent
        when you press it.
      </p>
      <div className="mt-2.5 flex flex-wrap gap-2">
        {onLogin && (
          <Button size="sm" variant={providerReady ? "outline" : "default"} onClick={onLogin}>
            <LogInIcon />
            Sign in
          </Button>
        )}
        {/* Appears once the instance reports ready again — the pushed
            harnesses frame after a sign-in is what flips it. Explicit by
            design: a finished flow never resends the prompt itself. */}
        {onRetryTurn && providerReady && (
          <Button
            size="sm"
            disabled={sending}
            onClick={() => {
              setSending(true);
              onRetryTurn(turn);
            }}
          >
            {sending ? "Sending…" : "Retry this prompt"}
          </Button>
        )}
      </div>
    </FailureBox>
  );
}

function recoveryNote(turn: Turn): string {
  if (!turn.recovery) return "The work was left unfinished.";
  if (turn.recovery.cause === "continue") return "Continuing it did not work either.";
  return "Picking it back up automatically did not work.";
}

function ErrorCard({
  turn,
  onContinue,
  sending,
  setSending,
}: Sending & Pick<InterruptedProps, "turn" | "onContinue">) {
  const error = turn.error ?? "";
  // The server says what kind of failure this was; reading the message for it
  // is how every death — a harness that exited, an account that needs to log
  // in again — came to be told as a story about a restart.
  const restarted = turn.failure === "restart";
  return (
    <FailureBox>
      <p className="text-[13px]">
        {restarted
          ? "The server restarted and this turn was interrupted before it finished."
          : "This turn ended with an error before it finished."}
      </p>
      {error && !restarted && (
        <p className="text-destructive mt-1.5 font-mono text-[11px] break-words">{error}</p>
      )}
      <p className="text-muted-foreground mt-1.5 text-[12px]">{recoveryNote(turn)}</p>
      <Button
        size="sm"
        className="mt-2.5"
        disabled={sending}
        onClick={() => {
          setSending(true);
          onContinue();
        }}
      >
        {sending ? "Continuing…" : "Continue where it left off"}
      </Button>
    </FailureBox>
  );
}

// InterruptedCard is what a turn that died looks like. A cross on the last
// tool call is not an explanation: it says something stopped, not that the
// work is unfinished and nobody is coming back for it. The server retries by
// itself after a restart, so this appears when that did not happen or did not
// work — which is precisely when a human has to decide.
export function InterruptedCard({ switchTargets = [], ...props }: InterruptedProps) {
  const [sending, setSending] = useState(false);
  if (props.turn.failure === "limit")
    return (
      <LimitCard
        {...props}
        switchTargets={switchTargets}
        sending={sending}
        setSending={setSending}
      />
    );
  if (props.turn.failure === "auth")
    return <LoginCard {...props} sending={sending} setSending={setSending} />;
  return <ErrorCard {...props} sending={sending} setSending={setSending} />;
}
