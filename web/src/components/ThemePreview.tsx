import { ArrowUpIcon, CheckIcon, ChevronDownIcon, FileDiffIcon, FolderIcon, GitBranchIcon, MoonIcon, PanelLeftIcon, PlusIcon, SearchIcon, SettingsIcon, TerminalIcon, XIcon } from "lucide-react";
import { useState, type CSSProperties } from "react";

import { ProviderLogo } from "~/components/ProviderLogo";
import { cn } from "~/lib/utils";

/**
 * The theme sample page, reachable at #themes: a static mock of the dashboard
 * behind a floating palette switcher, so candidate colour schemes can be
 * compared against real-looking content rather than swatches. Nothing here is
 * live — it renders canned threads and a canned conversation.
 *
 * Every palette redefines the same CSS custom properties the app's components
 * already consume, applied inline on the page wrapper; picking a winner means
 * copying its block into index.css.
 */
interface Palette {
  name: string;
  note: string;
  vars: Record<string, string>;
}

const PALETTES: Palette[] = [
  {
    name: "Current",
    note: "What ships today (dark) — Deep slate / raised",
    vars: {
      "--background": "oklch(0.19 0.01 265)",
      "--foreground": "oklch(0.93 0.008 265)",
      "--card": "oklch(0.23 0.012 265)",
      "--card-foreground": "oklch(0.93 0.008 265)",
      "--primary": "oklch(0.7 0.17 275)",
      "--primary-foreground": "oklch(0.97 0.005 265)",
      "--muted": "oklch(0.28 0.014 265)",
      "--muted-foreground": "oklch(0.66 0.02 265)",
      "--accent": "oklch(0.3 0.02 270)",
      "--accent-foreground": "oklch(0.93 0.008 265)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.36 0.09 275)",
      "--user-bubble-foreground": "oklch(0.95 0.015 275)",
      "--sidebar": "oklch(0.23 0.012 265)",
      "--sidebar-foreground": "oklch(0.93 0.008 265)",
      "--sidebar-accent": "oklch(0.3 0.02 270)",
      "--sidebar-accent-foreground": "oklch(0.95 0.01 265)",
      "--sidebar-border": "oklch(1 0 0 / 10%)",
      "--success": "oklch(0.74 0.14 160)",
    },
  },
  {
    name: "Deep slate",
    note: "Sidebar darker than the canvas, indigo accent",
    vars: {
      "--background": "oklch(0.19 0.01 265)",
      "--foreground": "oklch(0.93 0.008 265)",
      "--card": "oklch(0.23 0.012 265)",
      "--card-foreground": "oklch(0.93 0.008 265)",
      "--primary": "oklch(0.7 0.17 275)",
      "--primary-foreground": "oklch(0.97 0.005 265)",
      "--muted": "oklch(0.28 0.014 265)",
      "--muted-foreground": "oklch(0.66 0.02 265)",
      "--accent": "oklch(0.3 0.02 270)",
      "--accent-foreground": "oklch(0.93 0.008 265)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.36 0.09 275)",
      "--user-bubble-foreground": "oklch(0.95 0.015 275)",
      "--sidebar": "oklch(0.145 0.012 265)",
      "--sidebar-foreground": "oklch(0.9 0.01 265)",
      "--sidebar-accent": "oklch(0.24 0.02 270)",
      "--sidebar-accent-foreground": "oklch(0.95 0.01 265)",
      "--sidebar-border": "oklch(1 0 0 / 8%)",
      "--success": "oklch(0.74 0.14 160)",
    },
  },
  // Three takes on Deep slate that differ only in the sidebar: everything the
  // user liked stays fixed while the one part they didn't varies.
  {
    name: "Deep slate / flat",
    note: "Deep slate, sidebar same colour as the canvas",
    vars: {
      "--background": "oklch(0.19 0.01 265)",
      "--foreground": "oklch(0.93 0.008 265)",
      "--card": "oklch(0.23 0.012 265)",
      "--card-foreground": "oklch(0.93 0.008 265)",
      "--primary": "oklch(0.7 0.17 275)",
      "--primary-foreground": "oklch(0.97 0.005 265)",
      "--muted": "oklch(0.28 0.014 265)",
      "--muted-foreground": "oklch(0.66 0.02 265)",
      "--accent": "oklch(0.3 0.02 270)",
      "--accent-foreground": "oklch(0.93 0.008 265)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.36 0.09 275)",
      "--user-bubble-foreground": "oklch(0.95 0.015 275)",
      "--sidebar": "oklch(0.19 0.01 265)",
      "--sidebar-foreground": "oklch(0.93 0.008 265)",
      "--sidebar-accent": "oklch(0.27 0.018 270)",
      "--sidebar-accent-foreground": "oklch(0.95 0.01 265)",
      "--sidebar-border": "oklch(1 0 0 / 10%)",
      "--success": "oklch(0.74 0.14 160)",
    },
  },
  {
    name: "Deep slate / raised",
    note: "Deep slate, sidebar lighter than the canvas (card tone)",
    vars: {
      "--background": "oklch(0.19 0.01 265)",
      "--foreground": "oklch(0.93 0.008 265)",
      "--card": "oklch(0.23 0.012 265)",
      "--card-foreground": "oklch(0.93 0.008 265)",
      "--primary": "oklch(0.7 0.17 275)",
      "--primary-foreground": "oklch(0.97 0.005 265)",
      "--muted": "oklch(0.28 0.014 265)",
      "--muted-foreground": "oklch(0.66 0.02 265)",
      "--accent": "oklch(0.3 0.02 270)",
      "--accent-foreground": "oklch(0.93 0.008 265)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.36 0.09 275)",
      "--user-bubble-foreground": "oklch(0.95 0.015 275)",
      "--sidebar": "oklch(0.23 0.012 265)",
      "--sidebar-foreground": "oklch(0.93 0.008 265)",
      "--sidebar-accent": "oklch(0.3 0.02 270)",
      "--sidebar-accent-foreground": "oklch(0.95 0.01 265)",
      "--sidebar-border": "oklch(1 0 0 / 10%)",
      "--success": "oklch(0.74 0.14 160)",
    },
  },
  {
    name: "Deep slate / tinted",
    note: "Deep slate, sidebar washed with the indigo accent",
    vars: {
      "--background": "oklch(0.19 0.01 265)",
      "--foreground": "oklch(0.93 0.008 265)",
      "--card": "oklch(0.23 0.012 265)",
      "--card-foreground": "oklch(0.93 0.008 265)",
      "--primary": "oklch(0.7 0.17 275)",
      "--primary-foreground": "oklch(0.97 0.005 265)",
      "--muted": "oklch(0.28 0.014 265)",
      "--muted-foreground": "oklch(0.66 0.02 265)",
      "--accent": "oklch(0.3 0.02 270)",
      "--accent-foreground": "oklch(0.93 0.008 265)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.36 0.09 275)",
      "--user-bubble-foreground": "oklch(0.95 0.015 275)",
      "--sidebar": "oklch(0.21 0.035 272)",
      "--sidebar-foreground": "oklch(0.93 0.01 268)",
      "--sidebar-accent": "oklch(0.29 0.05 272)",
      "--sidebar-accent-foreground": "oklch(0.96 0.01 268)",
      "--sidebar-border": "oklch(1 0 0 / 9%)",
      "--success": "oklch(0.74 0.14 160)",
    },
  },
  {
    name: "Warm graphite",
    note: "Warm neutrals, amber accent — matches the Claude mark",
    vars: {
      "--background": "oklch(0.17 0.005 60)",
      "--foreground": "oklch(0.93 0.006 80)",
      "--card": "oklch(0.21 0.007 60)",
      "--card-foreground": "oklch(0.93 0.006 80)",
      "--primary": "oklch(0.74 0.13 60)",
      "--primary-foreground": "oklch(0.17 0.005 60)",
      "--muted": "oklch(0.27 0.008 60)",
      "--muted-foreground": "oklch(0.67 0.015 70)",
      "--accent": "oklch(0.29 0.012 60)",
      "--accent-foreground": "oklch(0.93 0.006 80)",
      "--border": "oklch(1 0 0 / 11%)",
      "--user-bubble": "oklch(0.34 0.05 60)",
      "--user-bubble-foreground": "oklch(0.95 0.01 70)",
      "--sidebar": "oklch(0.14 0.006 60)",
      "--sidebar-foreground": "oklch(0.91 0.008 80)",
      "--sidebar-accent": "oklch(0.25 0.014 60)",
      "--sidebar-accent-foreground": "oklch(0.95 0.008 80)",
      "--sidebar-border": "oklch(1 0 0 / 8%)",
      "--success": "oklch(0.74 0.14 155)",
    },
  },
  {
    name: "Forest",
    note: "Green-tinted neutrals, calm and low-glare",
    vars: {
      "--background": "oklch(0.18 0.012 165)",
      "--foreground": "oklch(0.93 0.01 160)",
      "--card": "oklch(0.22 0.014 165)",
      "--card-foreground": "oklch(0.93 0.01 160)",
      "--primary": "oklch(0.73 0.14 160)",
      "--primary-foreground": "oklch(0.16 0.012 165)",
      "--muted": "oklch(0.27 0.016 165)",
      "--muted-foreground": "oklch(0.66 0.025 160)",
      "--accent": "oklch(0.29 0.02 165)",
      "--accent-foreground": "oklch(0.93 0.01 160)",
      "--border": "oklch(1 0 0 / 10%)",
      "--user-bubble": "oklch(0.34 0.06 165)",
      "--user-bubble-foreground": "oklch(0.95 0.015 160)",
      "--sidebar": "oklch(0.145 0.014 165)",
      "--sidebar-foreground": "oklch(0.9 0.012 160)",
      "--sidebar-accent": "oklch(0.24 0.022 165)",
      "--sidebar-accent-foreground": "oklch(0.95 0.012 160)",
      "--sidebar-border": "oklch(1 0 0 / 8%)",
      "--success": "oklch(0.76 0.15 155)",
    },
  },
  {
    name: "Midnight violet",
    note: "Blue-violet cast, brighter primary",
    vars: {
      "--background": "oklch(0.17 0.02 295)",
      "--foreground": "oklch(0.93 0.01 295)",
      "--card": "oklch(0.21 0.024 295)",
      "--card-foreground": "oklch(0.93 0.01 295)",
      "--primary": "oklch(0.72 0.18 300)",
      "--primary-foreground": "oklch(0.97 0.005 295)",
      "--muted": "oklch(0.26 0.028 295)",
      "--muted-foreground": "oklch(0.67 0.03 295)",
      "--accent": "oklch(0.28 0.032 295)",
      "--accent-foreground": "oklch(0.93 0.01 295)",
      "--border": "oklch(1 0 0 / 11%)",
      "--user-bubble": "oklch(0.36 0.1 300)",
      "--user-bubble-foreground": "oklch(0.95 0.02 300)",
      "--sidebar": "oklch(0.135 0.022 295)",
      "--sidebar-foreground": "oklch(0.9 0.012 295)",
      "--sidebar-accent": "oklch(0.23 0.032 295)",
      "--sidebar-accent-foreground": "oklch(0.95 0.012 295)",
      "--sidebar-border": "oklch(1 0 0 / 8%)",
      "--success": "oklch(0.75 0.15 155)",
    },
  },
  {
    name: "Paper",
    note: "Light option: warm paper canvas, ink text",
    vars: {
      "--background": "oklch(0.985 0.004 85)",
      "--foreground": "oklch(0.24 0.01 60)",
      "--card": "oklch(1 0 0)",
      "--card-foreground": "oklch(0.24 0.01 60)",
      "--primary": "oklch(0.55 0.15 60)",
      "--primary-foreground": "oklch(0.99 0 0)",
      "--muted": "oklch(0.95 0.006 85)",
      "--muted-foreground": "oklch(0.5 0.015 60)",
      "--accent": "oklch(0.94 0.008 85)",
      "--accent-foreground": "oklch(0.28 0.01 60)",
      "--border": "oklch(0.88 0.008 85)",
      "--user-bubble": "oklch(0.93 0.03 70)",
      "--user-bubble-foreground": "oklch(0.32 0.04 60)",
      "--sidebar": "oklch(0.955 0.008 85)",
      "--sidebar-foreground": "oklch(0.24 0.01 60)",
      "--sidebar-accent": "oklch(0.9 0.012 85)",
      "--sidebar-accent-foreground": "oklch(0.28 0.01 60)",
      "--sidebar-border": "oklch(0.88 0.008 85)",
      "--success": "oklch(0.6 0.13 155)",
    },
  },
];

const FAKE_THREADS = [
  { harness: "claude", title: "Fix the flaky websocket test", project: "omniplex", branch: "feature/ws-retry", ago: "2m", active: true },
  { harness: "codex", title: "Add CSV export to reports", project: "reports", branch: "main", ago: "1h", active: false },
  { harness: "claude", title: "Untitled", project: "omniplex", branch: "feature/theme-page", ago: "3h", active: false },
];

export function ThemePreview() {
  const [active, setActive] = useState(0);
  const palette = PALETTES[active];

  return (
    <div
      style={palette.vars as CSSProperties}
      className="bg-background text-foreground relative flex h-full overflow-hidden"
    >
      {/* Floating switcher: click through the schemes against the same mock. */}
      <div className="bg-card/90 absolute top-3 left-1/2 z-30 flex -translate-x-1/2 items-center gap-1 rounded-full border px-2 py-1.5 shadow-lg backdrop-blur">
        {PALETTES.map((p, i) => (
          <button
            key={p.name}
            type="button"
            onClick={() => setActive(i)}
            title={p.note}
            className={cn(
              "cursor-pointer rounded-full px-2.5 py-1 text-[11px] whitespace-nowrap transition-colors",
              i === active ? "bg-primary text-primary-foreground" : "hover:bg-accent text-muted-foreground",
            )}
          >
            {p.name}
          </button>
        ))}
        <a
          href="#"
          aria-label="Close the theme preview"
          className="hover:bg-accent text-muted-foreground ml-1 flex size-6 items-center justify-center rounded-full"
        >
          <XIcon className="size-3.5" />
        </a>
      </div>

      {/* ---- Fake sidebar ---- */}
      <aside className="bg-sidebar text-sidebar-foreground flex w-72 shrink-0 flex-col border-r" style={{ borderColor: "var(--sidebar-border)" }}>
        <div className="flex items-center gap-2 px-3 pt-2 pb-1.5">
          <span className="flex-1 px-1.5 font-mono text-sm font-semibold tracking-tight">Omniplex</span>
          <span className="text-muted-foreground flex size-8 items-center justify-center">
            <PlusIcon className="size-4" />
          </span>
          <span className="text-muted-foreground flex size-8 items-center justify-center">
            <PanelLeftIcon className="size-4" />
          </span>
        </div>
        <div className="flex-1 px-2 py-2">
          {FAKE_THREADS.map((s) => (
            <div
              key={s.title}
              className={cn(
                "mb-0.5 rounded-lg px-2.5 py-2",
                s.active
                  ? "bg-sidebar-accent text-sidebar-accent-foreground"
                  : "hover:bg-sidebar-accent/60",
              )}
            >
              <span className="flex items-center gap-1.5">
                <span className="min-w-0 flex-1 truncate text-[13px]">{s.title}</span>
                {s.active && <span className="bg-primary size-1.5 shrink-0 animate-pulse rounded-full" />}
                <span className="text-muted-foreground shrink-0 font-mono text-[10px]">{s.ago}</span>
              </span>
              <span className="text-muted-foreground mt-1 flex min-w-0 items-center gap-1 font-mono text-[10px]">
                <FolderIcon className="size-3 shrink-0" />
                <span className="truncate">{s.project}</span>
                <GitBranchIcon className="ml-1 size-3 shrink-0" />
                <span className="truncate">{s.branch}</span>
                <span className="ml-auto flex shrink-0 items-center pl-1.5">
                  <ProviderLogo provider={s.harness} className="size-3.5" />
                </span>
              </span>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-2 px-3 py-2" style={{ borderTop: "1px solid var(--sidebar-border)" }}>
          <span className="text-muted-foreground flex-1 text-[11px]">3 threads</span>
          <span className="bg-success size-2 rounded-full" />
          <MoonIcon className="text-muted-foreground size-4" />
        </div>
      </aside>

      {/* ---- Fake content column ---- */}
      <main className="relative flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-2 px-3 pt-3 pb-2">
          <p className="min-w-0 flex-1 truncate text-[13px] font-medium">Fix the flaky websocket test</p>
          <FileDiffIcon className="text-muted-foreground size-4" />
          <SettingsIcon className="text-muted-foreground size-4" />
        </header>

        <div className="relative min-h-0 flex-1">
          <div className="from-background pointer-events-none absolute inset-x-0 top-0 z-10 h-8 bg-gradient-to-b to-transparent" />
          <div className="mx-auto flex h-full max-w-3xl flex-col gap-3.5 overflow-hidden px-5 pt-6">
            <div className="flex justify-end">
              <div className="bg-user-bubble text-user-bubble-foreground max-w-[85%] rounded-2xl rounded-br-md px-3.5 py-2 text-[14px] leading-relaxed">
                The websocket reconnect test fails about one run in five. Can you find out why and fix it?
              </div>
            </div>
            <p className="text-[14px] leading-relaxed">
              I'll start by reproducing it locally and pulling the retry timing apart — a one-in-five failure
              usually means a race between the reconnect backoff and the test's own timeout.
            </p>
            <div className="bg-card/60 flex items-center gap-2.5 rounded-lg border px-3 py-2">
              <SearchIcon className="text-muted-foreground size-3.5" />
              <span className="text-muted-foreground flex-1 truncate font-mono text-[13px]">grep -rn "reconnect" internal/server</span>
              <CheckIcon className="text-success size-3.5" />
            </div>
            <div className="bg-card/60 flex items-center gap-2.5 rounded-lg border px-3 py-2">
              <TerminalIcon className="text-muted-foreground size-3.5" />
              <span className="text-muted-foreground flex-1 truncate font-mono text-[13px]">go test -race -count=20 ./internal/server</span>
              <CheckIcon className="text-success size-3.5" />
            </div>
            <p className="text-[14px] leading-relaxed">
              Found it: the test asserts on the second attempt but the backoff jitter can push that attempt past
              the 100ms deadline. Pinning the jitter source in the test makes it deterministic.
            </p>
          </div>

          {/* Fake floating composer */}
          <div className="absolute inset-x-0 bottom-0 z-10">
            <div className="mx-auto max-w-3xl px-5 pb-4">
              <div className="bg-card rounded-2xl border shadow-lg">
                <p className="text-muted-foreground px-4 pt-3 pb-1 text-[14px]">Ask anything…</p>
                <div className="flex items-center gap-1 px-2.5 pb-2">
                  <span className="text-success flex size-8 items-center justify-center">
                    <svg viewBox="0 0 16 16" className="size-4 -rotate-90">
                      <circle cx="8" cy="8" r="6" fill="none" strokeWidth="2.5" className="stroke-border" />
                      <circle cx="8" cy="8" r="6" fill="none" strokeWidth="2.5" strokeLinecap="round" stroke="currentColor" strokeDasharray={2 * Math.PI * 6} strokeDashoffset={2 * Math.PI * 6 * 0.62} />
                    </svg>
                  </span>
                  <span className="flex-1" />
                  <span className="text-muted-foreground flex items-center gap-1 px-2 text-[12px]">
                    <span className="text-foreground/80">Opus</span> medium
                    <ChevronDownIcon className="size-3.5" />
                  </span>
                  <span className="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-full">
                    <ArrowUpIcon className="size-4" />
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}
