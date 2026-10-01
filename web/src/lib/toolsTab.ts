// Which tab of the Skills page was last open, so the page reopens on it.

export type ToolsTab = "skills" | "mcp" | "signins";

const KEY = "omniplex.tools.tab";
const TABS: string[] = ["skills", "mcp", "signins"];

export function loadToolsTab(): ToolsTab {
  try {
    const v = localStorage.getItem(KEY);
    return v && TABS.includes(v) ? (v as ToolsTab) : "skills";
  } catch {
    return "skills";
  }
}

export function saveToolsTab(tab: ToolsTab) {
  try {
    localStorage.setItem(KEY, tab);
  } catch {
    // Storage can be denied; the page still works, it just opens on Skills.
  }
}
