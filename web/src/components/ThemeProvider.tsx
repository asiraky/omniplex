import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import {
  applyTheme,
  applyTint,
  readStoredTheme,
  readStoredTint,
  resolveTheme,
  storeTheme,
  storeTint,
  ThemeContext,
  watchSystemTheme,
  type Theme,
  type Tint,
} from "~/lib/theme";

export function ThemeProvider({ children }: { children: ReactNode }) {
  // The inline script in index.html has already put the right class on <html>;
  // this reads the same store so React starts in agreement with the paint that
  // already happened rather than flipping it back.
  const [theme, setThemeState] = useState<Theme>(() => readStoredTheme());
  const [resolved, setResolved] = useState(() => resolveTheme(readStoredTheme()));

  useEffect(() => {
    const next = resolveTheme(theme);
    setResolved(next);
    applyTheme(next);
    // "system" is a live subscription, not a reading taken once at startup:
    // the OS can flip at sunset with the tab open, and the page follows.
    if (theme !== "system") return;
    return watchSystemTheme((system) => {
      setResolved(system);
      applyTheme(system);
    });
  }, [theme]);

  const setTheme = useCallback((next: Theme) => {
    storeTheme(next);
    setThemeState(next);
  }, []);

  const [tint, setTintState] = useState<Tint>(() => readStoredTint());
  useEffect(() => applyTint(tint), [tint]);
  const setTint = useCallback((next: Tint) => {
    storeTint(next);
    setTintState(next);
  }, []);

  const value = useMemo(
    () => ({ theme, resolved, setTheme, tint, setTint }),
    [theme, resolved, setTheme, tint, setTint],
  );

  return <ThemeContext value={value}>{children}</ThemeContext>;
}
