import { useContext, useEffect, useState, type ReactNode } from "react";

import { ThemeContext } from "~/lib/theme";

export function MermaidDiagram({
  code,
  copyButton,
}: {
  code: string;
  copyButton: ReactNode;
}) {
  const theme = useContext(ThemeContext)?.resolved ?? "light";
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
    </div>
  );
}
