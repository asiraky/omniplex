import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { TINTS, useTheme, type Theme, type Tint } from "~/lib/theme";
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
      <DropdownMenuContent align="end">
        {OPTIONS.map(({ value, label, Icon }) => (
          <DropdownMenuItem
            key={value}
            onSelect={() => setTheme(value)}
            data-active={theme === value || undefined}
            className="data-[active]:bg-accent data-[active]:text-accent-foreground"
          >
            <Icon />
            {label}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-muted-foreground text-[11px] font-normal">
          Tint
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup value={tint} onValueChange={(v) => setTint(v as Tint)}>
          {TINTS.map((t) => (
            <DropdownMenuRadioItem
              key={t.value}
              value={t.value}
              // Stays open, so the tints can be tried one after another.
              onSelect={(e) => e.preventDefault()}
              className="min-h-11 md:min-h-0"
            >
              <span
                aria-hidden
                className="size-3.5 rounded-full border border-white/15"
                style={{ background: t.swatch }}
              />
              {t.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
