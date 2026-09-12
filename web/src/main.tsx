import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import { App } from "./App";
import { ThemeProvider } from "./components/ThemeProvider";
import { Toaster } from "./components/ui/sonner";
import { TooltipProvider } from "./components/ui/tooltip";
import { registerServiceWorker } from "./lib/pwa";

// Registered outside the React tree and not awaited. Nothing on screen depends
// on it: the worker only makes the app open faster offline and lets it be told
// things while it is closed. A browser without one renders exactly the same app.
void registerServiceWorker();

/**
 * Takes the boot screen in index.html down.
 *
 * After a paint, not on render: React's render call returns before the browser
 * has drawn anything, so removing it there swaps one blank screen for another.
 * Two frames is the cheap, reliable way to wait for pixels.
 */
function clearBootScreen() {
  const boot = document.getElementById("boot");
  if (!boot) return;
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      boot.classList.add("done");
      // Long enough for the fade in the stylesheet, then gone from the tree —
      // a fixed overlay left in place would swallow taps forever.
      boot.addEventListener("transitionend", () => boot.remove(), { once: true });
      setTimeout(() => boot.remove(), 400);
    }),
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      {/* Tooltips are hints on icon-only controls, so they wait longer than
          the default before appearing and never fire on a touch tap. */}
      <TooltipProvider delayDuration={400}>
        <App />
        <Toaster position="top-center" />
      </TooltipProvider>
    </ThemeProvider>
  </StrictMode>,
);

clearBootScreen();
