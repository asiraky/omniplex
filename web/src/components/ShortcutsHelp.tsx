import { Fragment, type ReactNode } from "react";

import { Dialog, DialogContent, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Kbd, KbdChord } from "~/components/ui/kbd";
import {
  COMMAND_IDS,
  COMMANDS,
  commandLabel,
  currentPlatform,
  isElectronApp,
  jumpCommand,
  jumpIndex,
  shortcutsFor,
  type CommandGroup,
  type CommandSpec,
  type Platform,
} from "~/lib/keybindings";

const GROUPS: CommandGroup[] = ["Threads", "Composer", "Panels", "App"];

interface Row {
  label: string;
  keys: ReactNode;
}

/** Every command with a chord in this shell, by group, in table order. */
function rows(electronApp: boolean, platform: Platform): Map<CommandGroup, Row[]> {
  const byGroup = new Map<CommandGroup, Row[]>(GROUPS.map((g) => [g, []]));
  for (const id of COMMAND_IDS) {
    const spec: CommandSpec = COMMANDS[id];
    // The nine jumps are one row, where the first of them sits in the table.
    if (jumpIndex(id) === 0 && electronApp) {
      const first = shortcutsFor(id, true)[0];
      const last = shortcutsFor(jumpCommand(8)!, true)[0];
      byGroup.get(spec.group)!.push({
        label: "Go to thread 1–9",
        keys: (
          <>
            <KbdChord shortcut={first} platform={platform} />
            <span aria-hidden>–</span>
            <KbdChord shortcut={last} platform={platform} />
          </>
        ),
      });
    }
    if (spec.hidden) continue;
    const chords = shortcutsFor(id, electronApp);
    if (!chords.length) continue;
    byGroup.get(spec.group)!.push({
      label: spec.label,
      keys: chords.map((s, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="text-muted-foreground text-[11px]">or</span>}
          <KbdChord shortcut={s} platform={platform} />
        </Fragment>
      )),
    });
  }
  return byGroup;
}

/**
 * The keyboard shortcuts, on mod+/ or `?`. Only the chords this shell answers
 * to: a browser tab leaves out what the browser keeps for itself. A phone gets
 * the whole list too, full screen: an iPad with a keyboard reaches it from the
 * palette.
 */
export function ShortcutsHelp({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const electronApp = isElectronApp();
  const platform = currentPlatform();
  const groups = rows(electronApp, platform);
  const palette = commandLabel("palette", electronApp, platform);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        fullscreenOnMobile
        aria-describedby={undefined}
        className="flex max-h-[min(90dvh,44rem)] flex-col gap-0 p-0 md:max-w-lg"
      >
        <DialogHeader className="border-b px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
          {GROUPS.map((group) => {
            const list = groups.get(group)!;
            if (!list.length) return null;
            return (
              <section key={group} aria-labelledby={`shortcuts-${group}`}>
                <h3 id={`shortcuts-${group}`} className="text-muted-foreground mb-1.5 text-[11px] font-medium">
                  {group}
                </h3>
                <ul className="divide-y">
                  {list.map((row) => (
                    <li key={row.label} className="flex min-h-9 items-center justify-between gap-4 py-1.5 text-sm">
                      <span className="min-w-0">{row.label}</span>
                      <span className="flex shrink-0 items-center gap-1">{row.keys}</span>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>

        {palette && (
          <p className="text-muted-foreground border-t px-6 py-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))] text-[12px] md:pb-3">
            Also in the palette: <Kbd>{palette}</Kbd>
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}
