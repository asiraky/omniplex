import { LogInIcon, PlusIcon, RefreshCwIcon } from "lucide-react";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import type { PickerInstance } from "~/lib/models";
import type { HarnessMeta } from "~/protocol";

/**
 * Every account that cannot start, not just the chosen one: the picker falls
 * back to whatever is ready, so a signed-out account would otherwise vanish
 * with no word of why.
 */
export function InstanceAlerts({
  harnesses,
  instances,
  onLogin,
  onRecheck,
  onManageProviders,
  onAddAccount,
}: {
  harnesses: HarnessMeta[];
  instances: PickerInstance[];
  onLogin?: (instanceId: string) => void;
  onRecheck: () => void;
  onManageProviders?: () => void;
  /** Open adding an account: for this harness, or null to ask which. */
  onAddAccount?: (harnessId: string | null) => void;
}) {
  // No account anywhere: there is nothing to pick and nothing to sign in to,
  // so the one way forward is adding one. With a single agent on offer that
  // agent is preselected.
  if (instances.length === 0 && harnesses.length > 0) {
    const only = harnesses.length === 1 ? harnesses[0] : undefined;
    return (
      <Alert>
        <AlertDescription>
          <span>{only?.availability.reason || "No accounts yet. Add one to start a thread."}</span>
          {onAddAccount && (
            <div className="mt-2 flex flex-wrap gap-2">
              <Button size="sm" onClick={() => onAddAccount(only?.id ?? null)}>
                <PlusIcon />
                Add account
              </Button>
            </div>
          )}
        </AlertDescription>
      </Alert>
    );
  }
  return instances
    .filter((i) => i.enabled && i.availability?.state !== "ready")
    .map((i) => (
      <Alert key={i.id}>
        <AlertDescription>
          <span>
            {instances.length > 1 && <span className="font-medium">{i.name}: </span>}
            {i.availability?.reason}
          </span>
          <div className="mt-2 flex flex-wrap gap-2">
            {onLogin && i.availability?.remedy?.some((r) => r.action === "login") && (
              <Button size="sm" onClick={() => onLogin(i.id)}>
                <LogInIcon />
                Sign in
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={onRecheck}>
              <RefreshCwIcon />
              Check again
            </Button>
            {onManageProviders && (
              <Button variant="outline" size="sm" onClick={onManageProviders}>
                Providers…
              </Button>
            )}
          </div>
        </AlertDescription>
      </Alert>
    ));
}
