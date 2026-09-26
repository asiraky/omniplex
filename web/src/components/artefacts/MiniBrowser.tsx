import {
  ArrowLeftIcon,
  ArrowRightIcon,
  CheckIcon,
  MonitorIcon,
  RotateCwIcon,
  SmartphoneIcon,
  SquareTerminalIcon,
  TabletIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { IconButton } from "~/components/IconButton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Spinner } from "~/components/ui/spinner";
import { bundlePath, retoken, type ExpiringUrl } from "~/lib/artefacts";
import { cn } from "~/lib/utils";

export type Device = "desktop" | "tablet" | "phone";

const DEVICES: { id: Device; label: string; width?: number; Icon: typeof MonitorIcon }[] = [
  { id: "desktop", label: "Desktop", Icon: MonitorIcon },
  { id: "tablet", label: "Tablet · 768", width: 768, Icon: TabletIcon },
  { id: "phone", label: "Phone · 390", width: 390, Icon: SmartphoneIcon },
];

const DEVICE_KEY = "omniplex.artefactDevice";

function loadDevice(): Device {
  try {
    const v = localStorage.getItem(DEVICE_KEY);
    return v === "tablet" || v === "phone" ? v : "desktop";
  } catch {
    return "desktop";
  }
}

/** The page's width and the scale that fits it into the stage: a device wider
    than the panel is shrunk whole rather than cropped, so a phone can still
    see what a desktop layout looks like. */
export function frameFit(deviceWidth: number | undefined, stageWidth: number): { width?: number; scale: number } {
  if (!deviceWidth || stageWidth <= 0) return { scale: 1 };
  return { width: deviceWidth, scale: Math.min(1, stageWidth / deviceWidth) };
}

export interface ConsoleEntry {
  n: number;
  level: "log" | "info" | "warn" | "error";
  text: string;
}

// A page that logs in a loop must not grow the parent's memory without end.
const MAX_ENTRIES = 500;
const MAX_TEXT = 4000;
// Refresh a token this close to expiry rather than reload into a 404.
const EXPIRY_MARGIN_MS = 30_000;

/**
 * An HTML artefact in a small browser: back, forward, reload, where you are
 * inside the bundle, a device width, and the page's console.
 *
 * The page is in a sandboxed frame without `allow-same-origin`, so it runs in
 * an opaque origin and cannot reach this app, its cookie, or its API. It talks
 * back only through the bridge the preview route injects, whose messages are
 * accepted only from this frame's own window.
 */
export function MiniBrowser({
  preview,
  refreshPreview,
  onLocationChange,
  title,
}: {
  /** The freshest preview URL. A newer one does not reload the page; it is
      used for the next reload. */
  preview: ExpiringUrl;
  refreshPreview: () => Promise<ExpiringUrl>;
  onLocationChange?: (url: string) => void;
  title: string;
}) {
  const frameRef = useRef<HTMLIFrameElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef(preview);
  previewRef.current = preview;
  const onLocationRef = useRef(onLocationChange);
  onLocationRef.current = onLocationChange;

  const [src, setSrc] = useState(preview.url);
  const [frameKey, setFrameKey] = useState(0);
  const [location, setLocation] = useState(preview.url);
  const [pageTitle, setPageTitle] = useState("");
  const [canBack, setCanBack] = useState(false);
  const [forwardDepth, setForwardDepth] = useState(0);
  const [loading, setLoading] = useState(true);
  const [reloadError, setReloadError] = useState("");
  const lastAction = useRef<"back" | "forward" | null>(null);

  const [entries, setEntries] = useState<ConsoleEntry[]>([]);
  const [consoleOpen, setConsoleOpen] = useState(false);
  const seq = useRef(0);
  const errors = entries.filter((e) => e.level === "error").length;

  const [device, setDevice] = useState<Device>(loadDevice);
  const [stage, setStage] = useState({ width: 0, height: 0 });

  useLayoutEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const measure = () => setStage({ width: el.clientWidth, height: el.clientHeight });
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (!frameRef.current || e.source !== frameRef.current.contentWindow) return;
      const d = e.data as Record<string, unknown> | null;
      if (!d || typeof d !== "object") return;
      if (d.omniplex === "nav" && typeof d.url === "string") {
        setLocation(d.url);
        setPageTitle(typeof d.title === "string" ? d.title : "");
        if (typeof d.canBack === "boolean") setCanBack(d.canBack);
        const action = lastAction.current;
        lastAction.current = null;
        setForwardDepth((n) => (action === "back" ? n + 1 : action === "forward" ? Math.max(0, n - 1) : 0));
        onLocationRef.current?.(d.url);
        return;
      }
      if (d.omniplex === "console" && typeof d.text === "string") {
        const level: ConsoleEntry["level"] =
          d.level === "info" || d.level === "warn" || d.level === "error" ? d.level : "log";
        const text = d.text.length > MAX_TEXT ? `${d.text.slice(0, MAX_TEXT)}…` : d.text;
        setEntries((all) => [...all, { n: ++seq.current, level, text }].slice(-MAX_ENTRIES));
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  // The frame is opaque-origin, so the only target that reaches it is "*".
  // Nothing sent is secret: it is a request to go back or forward.
  const post = (message: { omniplex: string }) => frameRef.current?.contentWindow?.postMessage(message, "*");

  const back = () => {
    lastAction.current = "back";
    post({ omniplex: "back" });
  };
  const forward = () => {
    lastAction.current = "forward";
    post({ omniplex: "forward" });
  };

  const reload = useCallback(async () => {
    setReloadError("");
    let fresh = previewRef.current;
    if (Date.now() > fresh.expiresAt - EXPIRY_MARGIN_MS) {
      try {
        fresh = await refreshPreview();
      } catch (e) {
        setReloadError(e instanceof Error ? e.message : String(e));
        return;
      }
    }
    setEntries([]);
    setLoading(true);
    setSrc(retoken(location, fresh.url));
    // A new key remounts the frame, which reloads it even when the URL is the
    // one it already has.
    setFrameKey((k) => k + 1);
  }, [location, refreshPreview]);

  const chooseDevice = (d: Device) => {
    setDevice(d);
    try {
      localStorage.setItem(DEVICE_KEY, d);
    } catch {
      // The choice lasts this view.
    }
  };

  const spec = DEVICES.find((d) => d.id === device)!;
  const fit = frameFit(spec.width, stage.width);
  const path = bundlePath(location);

  // Smaller than IconButton's 44px on a phone: six controls and a URL have to
  // share one row at 390px. 36px is still a comfortable thumb.
  const chromeButton = "size-9 md:size-7";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-0.5 border-b px-1 py-1">
        <IconButton label="Back" onClick={back} disabled={!canBack} className={chromeButton}>
          <ArrowLeftIcon />
        </IconButton>
        <IconButton label="Forward" onClick={forward} disabled={forwardDepth === 0} className={chromeButton}>
          <ArrowRightIcon />
        </IconButton>
        <IconButton label="Reload" onClick={() => void reload()} className={chromeButton}>
          <RotateCwIcon className={cn(loading && "animate-spin")} />
        </IconButton>
        <span
          className="bg-muted text-muted-foreground mx-1 min-w-0 flex-1 truncate rounded-full px-3 py-1 font-mono text-[11px]"
          title={pageTitle ? `${pageTitle}\n${path}` : path}
          data-testid="url-pill"
        >
          {path}
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton label={`Width: ${spec.label}`} className={chromeButton}>
              <spec.Icon />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {DEVICES.map((d) => (
              <DropdownMenuItem key={d.id} onSelect={() => chooseDevice(d.id)} className="min-h-11 md:min-h-0">
                <d.Icon />
                <span className="flex-1">{d.label}</span>
                {d.id === device && <CheckIcon />}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <span className="relative">
          <IconButton
            label={consoleOpen ? "Hide the console" : "Show the console"}
            aria-pressed={consoleOpen}
            onClick={() => setConsoleOpen((v) => !v)}
            className={cn(chromeButton, consoleOpen && "bg-accent")}
          >
            <SquareTerminalIcon />
          </IconButton>
          {errors > 0 && (
            <span
              aria-label={`${errors} console errors`}
              className="bg-destructive pointer-events-none absolute top-0 right-0 min-w-4 rounded-full px-1 text-center text-[9px] leading-4 font-semibold text-white tabular-nums"
            >
              {errors > 99 ? "99+" : errors}
            </span>
          )}
        </span>
      </div>

      {reloadError && (
        <p role="alert" className="text-destructive border-b px-3 py-1.5 text-[11px]">
          Could not reload: {reloadError}
        </p>
      )}

      <div ref={stageRef} className="bg-muted/40 relative min-h-0 flex-1 overflow-hidden">
        <div
          className="mx-auto h-full"
          style={fit.width ? { width: fit.width * fit.scale } : { width: "100%" }}
        >
          <iframe
            key={frameKey}
            ref={frameRef}
            src={src}
            title={title}
            // Never allow-same-origin: with it, the page and this app would
            // share an origin, and the agent's HTML could drive the API.
            sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads"
            referrerPolicy="no-referrer"
            onLoad={() => setLoading(false)}
            className={cn("block border-0 bg-white", fit.width && "shadow-md")}
            style={
              fit.width
                ? {
                    width: fit.width,
                    height: stage.height / fit.scale,
                    transform: `scale(${fit.scale})`,
                    transformOrigin: "top left",
                  }
                : { width: "100%", height: "100%" }
            }
          />
        </div>
        {loading && (
          <span className="bg-background/80 absolute top-2 right-2 rounded-full p-1 shadow-sm">
            <Spinner className="text-primary size-3.5" />
          </span>
        )}
      </div>

      {consoleOpen && (
        <div className="flex max-h-[45%] min-h-32 flex-col border-t">
          <div className="flex items-center gap-1 border-b px-2 py-0.5">
            <span className="text-muted-foreground flex-1 text-[11px] font-medium">Console</span>
            <IconButton label="Clear the console" onClick={() => setEntries([])} className={chromeButton}>
              <Trash2Icon />
            </IconButton>
            <IconButton label="Hide the console" onClick={() => setConsoleOpen(false)} className={chromeButton}>
              <XIcon />
            </IconButton>
          </div>
          <ol aria-label="Console messages" className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain font-mono text-[11px]">
            {entries.length === 0 && (
              <li className="text-muted-foreground px-3 py-2 font-sans italic">Nothing logged.</li>
            )}
            {entries.map((e) => (
              <li
                key={e.n}
                className={cn(
                  "border-b px-3 py-1 break-words whitespace-pre-wrap",
                  e.level === "error" && "bg-destructive/10 text-destructive",
                  e.level === "warn" && "bg-attention/40",
                  e.level === "info" && "text-sky-700 dark:text-sky-300",
                )}
              >
                {e.text}
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  );
}

export default MiniBrowser;
