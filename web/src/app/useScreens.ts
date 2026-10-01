import { useEffect, useState } from "react";

import type { SettingsSection } from "~/components/SettingsScreen";
import type { ToolsTab } from "~/lib/toolsTab";

export type Screens = ReturnType<typeof useScreens>;

/** Which full-page screens and dialogs are up over the thread view. */
export function useScreens() {
  // Open, and at which section; the sidebar gear opens it at the top.
  const [settings, setSettings] = useState<{ at?: SettingsSection } | null>(null);
  const [newProject, setNewProject] = useState(false);
  const [manageLabels, setManageLabels] = useState(false);
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

  return {
    settings,
    setSettings,
    newProject,
    setNewProject,
    manageLabels,
    setManageLabels,
    showAccess,
    setShowAccess,
    showUsage,
    setShowUsage,
    tools,
    setTools,
    themePreview,
  };
}
