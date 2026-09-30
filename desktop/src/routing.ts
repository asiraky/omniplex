// Where the window goes once the server answers: the setup screen on the first
// launch, or whenever something the server needs (git, a harness) is missing;
// the app otherwise.
export type Route = "/" | "/setup";

export interface SetupStatus {
  ready: boolean;
}

export function parseSetup(body: unknown): SetupStatus | null {
  if (typeof body !== "object" || body === null) return null;
  const ready = (body as { ready?: unknown }).ready;
  return typeof ready === "boolean" ? { ready } : null;
}

// No setup status means a server that does not have the endpoint, so it has no
// setup page either; send the user to the app rather than a 404.
export function initialRoute(input: { firstRun: boolean; setup: SetupStatus | null }): Route {
  if (!input.setup) return "/";
  if (input.firstRun || !input.setup.ready) return "/setup";
  return "/";
}

// What the window does with a navigation it did not start itself.
export type NavigationDecision =
  | { kind: "allow" }
  | { kind: "external"; url: string }
  | { kind: "action"; action: DesktopAction }
  | { kind: "block" };

export type DesktopAction = "retry" | "show-log";
export const ACTION_SCHEME = "omniplex-desktop:";

export function classifyNavigation(url: string, serverOrigin: string | null): NavigationDecision {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return { kind: "block" };
  }
  if (parsed.protocol === ACTION_SCHEME) {
    const action = (parsed.hostname || parsed.pathname.replace(/^\/+/, "")) as DesktopAction;
    return action === "retry" || action === "show-log" ? { kind: "action", action } : { kind: "block" };
  }
  if (serverOrigin && parsed.origin === serverOrigin) return { kind: "allow" };
  if (parsed.protocol === "http:" || parsed.protocol === "https:" || parsed.protocol === "mailto:") {
    return { kind: "external", url: parsed.toString() };
  }
  return { kind: "block" };
}
