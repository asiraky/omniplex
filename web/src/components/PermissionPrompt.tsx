import { AttentionLabel } from "~/components/AttentionLabel";
import { Button } from "~/components/ui/button";
import type { PendingPermission } from "~/protocol";

// A permission request is durable thread state, not a modal owned by this
// connection: it is rendered from the log, and any attached device can answer.
export function PermissionPrompt({
  request,
  onResolve,
}: {
  request: PendingPermission;
  onResolve: (outcome: string, optionId: string) => void;
}) {
  const detail =
    request.input && typeof request.input === "object"
      ? JSON.stringify(request.input, null, 2)
      : null;

  return (
    // Floats in the band above the composer, where a thumb already rests, as a
    // rounded card matching the input rather than a full-bleed coloured tray.
    // Attention is a small pulsing dot, not a wall of amber, so it reads as
    // deliberate in both themes rather than glowing in one of them.
    <div className="mx-auto max-w-3xl px-4 pb-2.5 md:px-5">
      <div
        role="group"
        aria-label="Permission request"
        className="attention-in bg-card ring-attention/25 rounded-2xl border p-4 shadow-lg ring-1"
      >
        <AttentionLabel label="permission">
          <span className="text-muted-foreground font-mono text-[11px]">{request.toolName}</span>
        </AttentionLabel>

        <p className="mt-2 font-mono text-[13px] break-words">{request.title}</p>

        {detail && (
          <pre className="scroll-thin bg-muted text-muted-foreground mt-2 max-h-20 overflow-auto overscroll-contain rounded-md p-2 font-mono text-[11px] leading-relaxed md:max-h-32">
            {detail}
          </pre>
        )}

        <div className="mt-3 flex flex-wrap gap-2">
          {request.options.map((o) => (
            <Button
              key={o.optionId}
              // Allowing is the affirmative act and reads as the primary
              // button; anything else is a refusal and says so in red.
              variant={o.kind.startsWith("allow") ? "default" : "destructive"}
              className="min-h-11 flex-1 whitespace-nowrap md:min-h-9 md:flex-none"
              onClick={() => onResolve(o.kind, o.optionId)}
            >
              {o.name}
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}
