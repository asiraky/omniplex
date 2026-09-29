import { CheckIcon, MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { TINTS, useTheme, type Theme } from "~/lib/theme";
import { cn } from "~/lib/utils";

const OPTIONS: { value: Theme; label: string; Icon: typeof SunIcon }[] = [
  { value: "light", label: "Light", Icon: SunIcon },
  { value: "dark", label: "Dark", Icon: MoonIcon },
  { value: "system", label: "System", Icon: MonitorIcon },
];

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, setTheme, tint, setTint } = useTheme();
  const current = OPTIONS.find((o) => o.value === theme) ?? OPTIONS[2];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={cn("size-11 md:size-8", className)}
          // Icon-only, so the control has to say what it is out loud.
          aria-label={`Theme: ${current.label}`}
          title={`Theme: ${current.label}`}
        >
          <current.Icon />
        </Button>
      </DropdownMenuTrigger>
      {/* Both groups the same shape: what it is on the left, a tick on the
          right for the one in use. A radio dot in its own gutter pushed the
          tints out of line with the themes above them. */}
      <DropdownMenuContent align="end" className="min-w-40">
        {OPTIONS.map(({ value, label, Icon }) => (
          <Choice key={value} checked={theme === value} onSelect={() => setTheme(value)}>
            <Icon />
            {label}
          </Choice>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-muted-foreground text-[11px] font-normal">
          Tint
        </DropdownMenuLabel>
        {TINTS.map((t) => (
          <Choice
            key={t.value}
            checked={tint === t.value}
            onSelect={(e) => {
              // Stays open, so the tints can be tried one after another.
              e.preventDefault();
              setTint(t.value);
            }}
          >
            <span
              aria-hidden
              className="size-4 shrink-0 rounded-full border border-black/10 dark:border-white/15"
              style={{ background: t.swatch }}
            />
            {t.label}
          </Choice>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Choice({
  checked,
  onSelect,
  children,
}: {
  checked: boolean;
  onSelect: (e: Event) => void;
  children: ReactNode;
}) {
  return (
    <DropdownMenuItem
      role="menuitemradio"
      aria-checked={checked}
      onSelect={onSelect}
      className="min-h-11 md:min-h-0"
    >
      {children}
      <CheckIcon className={cn("ml-auto", !checked && "invisible")} />
    </DropdownMenuItem>
  );
}
