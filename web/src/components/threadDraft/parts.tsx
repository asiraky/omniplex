import type { ReactNode } from "react";

import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";
import { chipClass } from "./shared";

export function Chip({
  label,
  icon,
  children,
  className,
  ...props
}: {
  label: string;
  icon: ReactNode;
  children: ReactNode;
} & React.ComponentProps<"button">) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label={`${label}: ${typeof children === "string" ? children : ""}`.trim()}
      className={cn(chipClass, className)}
      {...props}
    >
      {icon}
      <span className="truncate">{children}</span>
    </Button>
  );
}

export function Described({ title, hint }: { title: string; hint?: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span className="text-[13px]">{title}</span>
      {hint && <span className="text-muted-foreground truncate text-[11px]">{hint}</span>}
    </span>
  );
}
