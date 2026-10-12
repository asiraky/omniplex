import { MaximizeIcon } from "lucide-react";
import {
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { ThemeContext } from "~/lib/theme";
import { MermaidViewer } from "./MermaidViewer";

export function MermaidDiagram({
  code,
  copyButton,
}: {
  code: string;
  copyButton: ReactNode;
}) {
  const theme = useContext(ThemeContext)?.resolved ?? "light";
  const [expandedImage, setExpandedImage] = useState<string | null>(null);
  const expandButton = useRef<HTMLButtonElement>(null);
  const viewerOpen = useRef(false);
  const ownsFullscreen = useRef(false);
  const enteredFullscreen = useRef(false);
  const requestGeneration = useRef(0);
  const pendingExit = useRef<Promise<void> | null>(null);

  const leaveFullscreen = useCallback(() => {
    if (!ownsFullscreen.current) return;
    ownsFullscreen.current = false;
    if (
      pendingExit.current ||
      document.fullscreenElement !== document.documentElement
    )
      return;
    const exit = document.exitFullscreen().catch(() => {});
    pendingExit.current = exit;
    void exit.then(() => {
      if (pendingExit.current === exit) pendingExit.current = null;
    });
  }, []);

  function close() {
    requestGeneration.current++;
    viewerOpen.current = false;
    setExpandedImage(null);
    leaveFullscreen();
    enteredFullscreen.current = false;
  }

  useEffect(() => {
    const changed = () => {
      if (!ownsFullscreen.current) return;
      if (document.fullscreenElement === document.documentElement)
        enteredFullscreen.current = true;
      else if (enteredFullscreen.current) {
        requestGeneration.current++;
        viewerOpen.current = false;
        ownsFullscreen.current = false;
        enteredFullscreen.current = false;
        setExpandedImage(null);
      }
    };
    document.addEventListener("fullscreenchange", changed);
    return () => {
      document.removeEventListener("fullscreenchange", changed);
      requestGeneration.current++;
      viewerOpen.current = false;
      leaveFullscreen();
    };
  }, [leaveFullscreen]);
  const [result, setResult] = useState<{
    code: string;
    theme: string;
    image: string | null;
  } | null>(null);

  useEffect(() => {
    let cancelled = false;
    // Streaming updates should not enqueue a render for every character.
    const timer = setTimeout(() => {
      void import("~/lib/mermaid")
        .then(({ renderMermaid }) =>
          renderMermaid(code, theme, () => cancelled),
        )
        .then(
          (image) => {
            if (!cancelled) setResult({ code, theme, image });
          },
          () => {
            if (!cancelled) setResult({ code, theme, image: null });
          },
        );
    }, 200);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [code, theme]);

  const current =
    result?.code === code && result.theme === theme ? result : null;
  const source = (
    <pre className="scroll-thin overflow-x-auto overscroll-x-contain p-2.5 font-mono text-[12px] leading-relaxed">
      <code>{code}</code>
    </pre>
  );

  return (
    <div className="bg-muted/60 my-2 overflow-hidden rounded-lg border">
      <div className="text-muted-foreground flex items-center gap-2 border-b px-2.5 py-1">
        <span className="min-w-0 flex-1 truncate font-mono text-[10px]">
          mermaid
        </span>
        {copyButton}
        {current?.image && (
          <button
            ref={expandButton}
            type="button"
            aria-label="Expand diagram to full screen"
            title="Full screen"
            className="hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring flex size-11 shrink-0 items-center justify-center rounded-md outline-none focus-visible:ring-2 md:size-6"
            onClick={() => {
              const generation = ++requestGeneration.current;
              viewerOpen.current = true;
              enteredFullscreen.current = false;
              setExpandedImage(current.image);
              const root = document.documentElement;
              const request = () => {
                if (
                  generation !== requestGeneration.current ||
                  !viewerOpen.current
                )
                  return;
                if (document.fullscreenElement || !root.requestFullscreen)
                  return;
                ownsFullscreen.current = true;
                // Request inside the click, while browser user activation is live.
                void root
                  .requestFullscreen()
                  .then(() => {
                    if (document.fullscreenElement === root) {
                      ownsFullscreen.current = true;
                      enteredFullscreen.current = true;
                      if (!viewerOpen.current) leaveFullscreen();
                    }
                  })
                  .catch(() => {
                    if (
                      generation === requestGeneration.current &&
                      document.fullscreenElement !== root
                    ) {
                      ownsFullscreen.current = false;
                    }
                  });
              };
              // A rapid reopen must wait for our previous exit to complete.
              if (pendingExit.current) void pendingExit.current.then(request);
              else request();
            }}
          >
            <MaximizeIcon className="size-3" />
          </button>
        )}
      </div>
      {current?.image ? (
        <>
          <div
            className="scroll-thin scroll-shadow-x overflow-x-auto overscroll-x-contain p-3"
            tabIndex={0}
            role="region"
            aria-label="Mermaid diagram"
          >
            <img
              src={current.image}
              alt="Mermaid diagram"
              className="mx-auto max-w-none"
            />
          </div>
          <details className="border-t">
            <summary className="text-muted-foreground focus-visible:ring-ring cursor-pointer px-2.5 py-2 text-xs outline-none focus-visible:ring-2">
              Diagram source
            </summary>
            {source}
          </details>
        </>
      ) : (
        <>
          <p
            className="text-muted-foreground px-2.5 pt-2 text-xs"
            role="status"
          >
            {current
              ? "Couldn't render this diagram. Source shown below."
              : "Rendering diagram…"}
          </p>
          {source}
        </>
      )}
      {expandedImage && (
        <MermaidViewer
          image={current?.image ?? expandedImage}
          onClose={close}
          onReturnFocus={() => expandButton.current?.focus()}
        />
      )}
    </div>
  );
}
