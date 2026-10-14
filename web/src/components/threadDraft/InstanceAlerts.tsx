import { LogInIcon, PlusIcon, RefreshCwIcon } from "lucide-react";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import type { PickerInstance } from "~/lib/models";

/**
 * Every account that cannot start, not just the chosen one: the picker falls
 * back to whatever is ready, so a signed-out account would otherwise vanish
 * with no word of why. With no account at all there is nothing to sign in to,
 * so the one way forward is adding one.
 */
export function InstanceAlerts({
  instances,
  onLogin,
  onRecheck,
  onManageProviders,
  onAddAccount,
}: {
  instances: PickerInstance[];
  onLogin?: (instanceId: string) => void;
  onRecheck: () => void;
  onManageProviders?: () => void;
  /** Open Settings → Providers on its add wizard. */
  onAddAccount?: () => void;
}) {
  if (instances.length === 0) {
    return (
      <Alert>
        <AlertDescription>
          <span>No accounts yet. Add one in Settings → Providers to start a thread.</span>
          {(onAddAccount ?? onManageProviders) && (
            <div className="mt-2 flex flex-wrap gap-2">
              <Button size="sm" onClick={onAddAccount ?? onManageProviders}>
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
