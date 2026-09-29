import { createContext, useContext } from "react";

export type Theme = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

/**
 * Shared with the inline boot script in index.html, which applies the stored
 * choice before first paint. Change it in one place and the other is wrong, so
 * the two are commented as a pair.
 */
export const THEME_STORAGE_KEY = "omniplex.theme";
/** Also read by the boot script in index.html. */
export const TINT_STORAGE_KEY = "omniplex.tint";

/**
 * The colour dark mode's greys lean towards. index.css turns each into a hue
 * and a strength; slate is the default and needs no attribute.
 */
export type Tint = "slate" | "blue" | "violet" | "green" | "warm" | "red" | "grey";

export const TINTS: { value: Tint; label: string; swatch: string }[] = [
  { value: "slate", label: "Slate", swatch: "oklch(0.42 0.04 265)" },
  { value: "blue", label: "Blue", swatch: "oklch(0.42 0.08 245)" },
  { value: "violet", label: "Violet", swatch: "oklch(0.42 0.08 295)" },
  { value: "green", label: "Green", swatch: "oklch(0.42 0.06 160)" },
  { value: "warm", label: "Warm", swatch: "oklch(0.42 0.05 55)" },
  { value: "red", label: "Red", swatch: "oklch(0.42 0.08 20)" },
  { value: "grey", label: "Grey", swatch: "oklch(0.42 0 0)" },
];

const DARK_QUERY = "(prefers-color-scheme: dark)";

export function prefersDark(): boolean {
  return window.matchMedia(DARK_QUERY).matches;
}

export function watchSystemTheme(onChange: (resolved: ResolvedTheme) => void): () => void {
  const mq = window.matchMedia(DARK_QUERY);
  const handler = (e: MediaQueryListEvent) => onChange(e.matches ? "dark" : "light");
  mq.addEventListener("change", handler);
  return () => mq.removeEventListener("change", handler);
}

/**
 * Storage access throws outright in a browser with cookies blocked, and the
 * theme is not worth taking the whole app down for: an unreadable store just
 * means "system", and an unwritable one means the choice lasts this tab only.
 * The inline boot script makes the same allowance.
 */
export function readStoredTheme(): Theme {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(THEME_STORAGE_KEY);
  } catch {
    return "system";
  }
  return stored === "light" || stored === "dark" || stored === "system" ? stored : "system";
}

export function storeTheme(theme: Theme) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    /* Not persisted. The choice still applies for this page. */
  }
}

export function readStoredTint(): Tint {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(TINT_STORAGE_KEY);
  } catch {
    return "slate";
  }
  return TINTS.find((t) => t.value === stored)?.value ?? "slate";
}

export function storeTint(tint: Tint) {
  try {
    localStorage.setItem(TINT_STORAGE_KEY, tint);
  } catch {
    /* Not persisted. The choice still applies for this page. */
  }
}

export function resolveTheme(theme: Theme): ResolvedTheme {
  return theme === "system" ? (prefersDark() ? "dark" : "light") : theme;
}

/**
 * The one place the class is written. The browser chrome is coloured from the
 * theme's own background rather than a second hardcoded literal, so the status
 * bar on a phone cannot drift out of step with the page behind it.
 */
export function applyTheme(resolved: ResolvedTheme) {
  const root = document.documentElement;
  root.classList.toggle("dark", resolved === "dark");
  root.style.colorScheme = resolved;
  colourBrowserChrome();
}

export function applyTint(tint: Tint) {
  const root = document.documentElement;
  if (tint === "slate") delete root.dataset.tint;
  else root.dataset.tint = tint;
  colourBrowserChrome();
}

function colourBrowserChrome() {
  const root = document.documentElement;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    const background = getComputedStyle(root).getPropertyValue("--background").trim();
    if (background) meta.setAttribute("content", background);
  }
}

export interface ThemeContextValue {
  theme: Theme;
  resolved: ResolvedTheme;
  setTheme: (theme: Theme) => void;
  tint: Tint;
  setTint: (tint: Tint) => void;
}

export const ThemeContext = createContext<ThemeContextValue | null>(null);

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("useTheme must be used inside <ThemeProvider>");
  return value;
}
