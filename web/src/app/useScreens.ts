import { useCallback, useEffect, useRef, useState } from "react";

import type { SettingsSection } from "~/components/SettingsScreen";
import { parseAgentPage, agentPagePath, type AgentPage } from "~/lib/agentRoutes";
import { isDraftEntry, navigate, onMove, useBackDismiss } from "~/lib/history";
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
  // The shell on the box itself, in the main pane beside the sidebar rather
  // than over the whole page: it is reached from the list and left back to it.
  // The open thread stays selected underneath, so closing returns to it.
  const [boxTerminal, setBoxTerminal] = useState(false);
  // None is a dialog, but on a phone each covers the thread like one, so
  // Back closes it rather than changing thread.
  useBackDismiss(showUsage, () => setShowUsage(false));
  useBackDismiss(!!tools, () => setTools(null));
  useBackDismiss(boxTerminal, () => setBoxTerminal(false));
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
    window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
    setThemePreview(false);
  }, []);

  // The first-run setup page at /setup: what this computer still needs before
  // a thread can start. The only screen with a path of its own, because the
  // desktop app opens it by URL.
  const [setup, setSetup] = useState(() => window.location.pathname === SETUP_PATH);
  useEffect(() => {
    return onMove(() => setSetup(window.location.pathname === SETUP_PATH));
  }, []);
  // Replaces rather than pushes: leaving setup is moving on, and Back landing
  // on it again would be a step backwards.
  const leaveSetup = useCallback(() => {
    window.history.replaceState(window.history.state, "", "/");
    setSetup(false);
  }, []);

  // An agent's page or the New agent form, in the main column beside the
  // sidebar like the box terminal. Unlike it, each has a path of its own
  // (lib/agentRoutes.ts), so a reload and Back and Forward land on it. The
  // thread attached before stays attached underneath.
  const [page, setPage] = useState<AgentPage | null>(currentPage);
  // The entry the page was pushed over, when it was a place of its own rather
  // than a draft the push took over: leaving the page for that same place is
  // then a Back, not a second entry for it.
  const pushedFrom = useRef<string | null>(null);
  const pageRef = useRef(page);
  useEffect(() => {
    pageRef.current = page;
  }, [page]);
  useEffect(() => {
    return onMove(() => {
      pushedFrom.current = null;
      setPage(currentPage());
    });
  }, []);

  // A full-page screen is up instead of the thread view, composer and all.
  // The box terminal and the agent pages count: they take the thread view's
  // place, if not the sidebar's.
  const fullPage = showUsage || !!tools || setup || themePreview || boxTerminal || !!page;
  // Whether a full-page screen or Settings hides the thread view, so the
  // server knows the open thread is not actually being watched.
  const covering = !!settings || fullPage;
  // Clear the screens a tapped notification should not open behind. Setup and
  // the theme sample are left alone: both are pages reached by URL.
  const uncover = useCallback(() => {
    setSettings(null);
    setShowUsage(false);
    setTools(null);
    setBoxTerminal(false);
  }, []);
  // Usage and Skills render over everything, so the terminal clears them to
  // be seen at all; Settings would sit over it as a dialog.
  const openBoxTerminal = useCallback(() => {
    uncover();
    setBoxTerminal(true);
  }, [uncover]);
  const closeBoxTerminal = useCallback(() => setBoxTerminal(false), []);

  // Another tab or focus of the page already open replaces its entry, so Back
  // leaves the page rather than stepping through its tabs. Anything else is a
  // place of its own, unless the caller replaces: the New agent form gives
  // way to the agent it made, and Back from there should not reopen the form.
  const openPage = useCallback(
    (next: AgentPage, { replace = false }: { replace?: boolean } = {}) => {
      uncover();
      const cur = pageRef.current;
      const same =
        !!cur &&
        cur.kind === next.kind &&
        (next.kind === "new" || (cur.kind === "agent" && cur.agentId === next.agentId));
      if (!same && !replace) {
        const { pathname, search } = window.location;
        pushedFrom.current = cur || isDraftEntry() ? null : pathname + search;
      }
      pageRef.current = next;
      setPage(next);
      navigate(agentPagePath(next), { replace: same || replace });
    },
    [uncover],
  );
  // Put the page away, leaving the URL to whoever is going somewhere next.
  const dropPage = useCallback(() => {
    pageRef.current = null;
    setPage(null);
  }, []);
  // Put the page away and go to `to`: Back, when that is the entry the page
  // was opened over, so history keeps one entry per place.
  // With replace, the page's entry becomes `to`: the form that made a thread
  // is done, and Back from the thread should not bring it back.
  const closePage = useCallback(
    (to: string, { draft = false, replace = false }: { draft?: boolean; replace?: boolean } = {}) => {
      const back = !replace && pushedFrom.current === to;
      pushedFrom.current = null;
      pageRef.current = null;
      setPage(null);
      if (back) window.history.back();
      else navigate(to, { draft, replace });
    },
    [],
  );

  // Choosing a thread or starting one is going somewhere: every screen gives
  // way, the URL ones too, since the user asked for it. An agent page's URL is
  // the caller's to replace, since it is going somewhere with a path.
  const leaveAll = () => {
    uncover();
    dropPage();
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
    page,
    openPage,
    dropPage,
    closePage,
    boxTerminal,
    openBoxTerminal,
    closeBoxTerminal,
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

function currentPage(): AgentPage | null {
  return parseAgentPage(window.location.pathname, window.location.search);
}
