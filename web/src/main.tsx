import { type ComponentType, lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import type { ToasterProps } from "sonner";
import "./index.css";
import { App } from "./App";
import { ThemeProvider } from "./components/ThemeProvider";
import { TooltipProvider } from "./components/ui/tooltip";

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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      {/* Tooltips are hints on icon-only controls, so they wait longer than
          the default before appearing and never fire on a touch tap. */}
      <TooltipProvider delayDuration={400}>
        <App />
        <Suspense fallback={null}>
          <Toaster position="top-center" />
        </Suspense>
      </TooltipProvider>
    </ThemeProvider>
  </StrictMode>,
);
