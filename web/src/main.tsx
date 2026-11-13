import { type ComponentType, lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import type { ToasterProps } from "sonner";
import "./index.css";
import { App } from "./App";
import { ThemeProvider } from "./components/ThemeProvider";
import { TooltipProvider } from "./components/ui/tooltip";
import { registerServiceWorker } from "./lib/threadLinks";
import { guardFileDrops } from "./useFileDrop";

// Toasts only ever report something that happened after load, so the Toaster
// arrives after first paint; see lib/toast.ts. With no error boundary above it,
// a failed fetch on a bad link would take the app down, so it gives up quietly.
const NoToaster: ComponentType<ToasterProps> = () => null;
const Toaster = lazy(() =>
  import("./components/ui/sonner").then(
    (m): { default: ComponentType<ToasterProps> } => ({ default: m.Toaster }),
    () => ({ default: NoToaster }),
  ),
);

// Push notifications need a worker registered before Settings can subscribe,
// and a tapped notification is routed by it.
registerServiceWorker();

// A file dropped where nothing takes it would otherwise replace the app.
guardFileDrops(window);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      {/* Tooltips are hints on icon-only controls, so they wait longer than
          the default before appearing and never fire on a touch tap. */}
      <TooltipProvider delayDuration={400}>
        <App />
        <Suspense fallback={null}>
          {/* Top on a phone keeps toasts clear of the composer and keyboard;
              the inset keeps them out from under the status bar of a
              home-screen install, which draws edge to edge. Sonner's own
              gaps, 24px and 16px below 600px wide, plus the inset. */}
          <Toaster
            position="top-center"
            offset={{ top: "calc(24px + env(safe-area-inset-top))" }}
            mobileOffset={{ top: "calc(16px + env(safe-area-inset-top))" }}
          />
        </Suspense>
      </TooltipProvider>
    </ThemeProvider>
  </StrictMode>,
);
