import { ChevronLeftIcon } from "lucide-react";
import type { ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { Alert, AlertDescription } from "~/components/ui/alert";

/**
 * One section of the settings screen: a heading, a body that scrolls, and an
 * optional footer that does not. `onBack` is the phone's way back to the list
 * of sections, or a section's own way out of a sub-view.
 */
export function SettingsPane({
  title,
  description,
  onBack,
  error,
  footer,
  children,
}: {
  title: string;
  description?: string;
  onBack?: () => void;
  /** Shown between the body and the footer, so it is seen without scrolling. */
  error?: string | null;
  footer?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex items-start gap-1 border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 md:pt-4 md:pr-14">
        {onBack && (
          <IconButton label="Back" onClick={onBack} className="-my-1.5 -ml-3 shrink-0">
            <ChevronLeftIcon />
          </IconButton>
        )}
        <div className="min-w-0">
          <h2 className="truncate text-lg leading-tight font-semibold">{title}</h2>
          {description && <p className="text-muted-foreground mt-1 text-sm">{description}</p>}
        </div>
      </header>

      <div className="scroll-thin min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
        {children}
      </div>

      {error && (
        <Alert variant="destructive" className="mx-6 mb-3 w-auto">
          <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
        </Alert>
      )}

      {footer && (
        <footer className="flex justify-end gap-2 border-t px-6 py-4 pb-[calc(1rem+env(safe-area-inset-bottom))] md:pb-4">
          {footer}
        </footer>
      )}
    </section>
  );
}
