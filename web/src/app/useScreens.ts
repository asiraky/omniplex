import { useCallback, useEffect, useState } from "react";

import type { SettingsSection } from "~/components/SettingsScreen";
import type { ToolsTab } from "~/lib/toolsTab";

export const SETUP_PATH = "/setup";

export type Screens = ReturnType<typeof useScreens>;

/** Which full-page screens and dialogs are up over the thread view. */
export function useScreens() {
  // Open, and at which section; the sidebar gear opens it at the top.
  const [settings, setSettings] = useState<{ at?: SettingsSection } | null>(null);
  const [newProject, setNewProject] = useState(false);
  const [showAccess, setShowAccess] = useState(false);
  // The account-level Usage page: cost history, token history, and the
  // providers' remaining allowance. A full-page destination, not a thread
  // view: it never needs one attached.
  const [showUsage, setShowUsage] = useState(false);
  // The Skills page and its MCP and Sign-ins tabs, reachable with no thread
  // open because these are the user's and the project's first. Open, and on
  // which tab when the caller cares; else the last one used.
  const [tools, setTools] = useState<{ tab?: ToolsTab } | null>(null);
  // The theme sample page: a static mock of the dashboard behind a palette
  // switcher, reachable at #themes so it needs no router.
  const [themePreview, setThemePreview] = useState(() => window.location.hash === "#themes");
  useEffect(() => {
    const onHash = () => setThemePreview(window.location.hash === "#themes");
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  // Drops the hash without a history entry, which fires no hashchange.
  const leaveThemePreview = useCallback(() => {
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    setThemePreview(false);
  }, []);

  // The first-run setup page at /setup: what this computer still needs before
  // a thread can start. The only screen with a path of its own, because the
  // desktop app opens it by URL.
  const [setup, setSetup] = useState(() => window.location.pathname === SETUP_PATH);
  useEffect(() => {
    const onPop = () => setSetup(window.location.pathname === SETUP_PATH);
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  // Replaces rather than pushes: leaving setup is moving on, and Back landing
  // on it again would be a step backwards.
  const leaveSetup = useCallback(() => {
    window.history.replaceState(null, "", "/");
    setSetup(false);
  }, []);

  // A full-page screen is up instead of the thread view, composer and all.
  const fullPage = showUsage || !!tools || setup || themePreview;
  // Whether a full-page screen or Settings hides the thread view, so the
  // server knows the open thread is not actually being watched.
  const covering = !!settings || fullPage;
  // Clear the screens a tapped notification should not open behind. Setup and
  // the theme sample are left alone: both are pages reached by URL.
  const uncover = useCallback(() => {
    setSettings(null);
    setShowUsage(false);
    setTools(null);
  }, []);
  // Choosing a thread or starting one is going somewhere: every screen gives
  // way, the URL ones too, since the user asked for it.
  const leaveAll = () => {
    uncover();
    if (setup) leaveSetup();
    if (themePreview) leaveThemePreview();
  };

  // The sidebar's buttons, made once: it is memoised, and an arrow written
  // inline in App would be a new prop on every App render.
  const openAccess = useCallback(() => setShowAccess(true), []);
  const openUsage = useCallback(() => setShowUsage(true), []);
  const openSkills = useCallback(() => setTools({}), []);
  const openSettings = useCallback(() => setSettings({}), []);
  const openNewProject = useCallback(() => setNewProject(true), []);

  return {
    fullPage,
    openAccess,
    openUsage,
    openSkills,
    openSettings,
    openNewProject,
    covering,
    uncover,
    leaveAll,
    settings,
    setSettings,
    newProject,
    setNewProject,
    showAccess,
    setShowAccess,
    showUsage,
    setShowUsage,
    tools,
    setTools,
    themePreview,
    leaveThemePreview,
    setup,
    leaveSetup,
  };
}
