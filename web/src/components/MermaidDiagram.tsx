import { MaximizeIcon } from "lucide-react";
import { useContext, useEffect, useRef, useState, type ReactNode } from "react";

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
  const active = useRef(false);
  const native = useRef(false);
  const entered = useRef(false);

  function close() {
    active.current = false;
    setExpandedImage(null);
    if (
      native.current &&
      document.fullscreenElement === document.documentElement
    ) {
      void document.exitFullscreen().catch(() => {});
    }
    native.current = false;
    entered.current = false;
  }

  useEffect(() => {
    const changed = () => {
      if (!native.current) return;
      if (document.fullscreenElement === document.documentElement)
        entered.current = true;
      else if (entered.current) {
        active.current = false;
        native.current = false;
        entered.current = false;
        setExpandedImage(null);
      }
    };
    document.addEventListener("fullscreenchange", changed);
    return () => {
      document.removeEventListener("fullscreenchange", changed);
      active.current = false;
      if (
        native.current &&
        document.fullscreenElement === document.documentElement
      ) {
        void document.exitFullscreen().catch(() => {});
      }
    };
  }, []);
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
              active.current = true;
              entered.current = false;
              setExpandedImage(current.image);
              const root = document.documentElement;
              if (!document.fullscreenElement && root.requestFullscreen) {
                native.current = true;
                // Request inside the click, while browser user activation is live.
                void root
                  .requestFullscreen()
                  .then(() => {
                    if (
                      !active.current &&
                      document.fullscreenElement === root
                    ) {
                      void document.exitFullscreen().catch(() => {});
                    }
                  })
                  .catch(() => {
                    native.current = false;
                  });
              }
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
