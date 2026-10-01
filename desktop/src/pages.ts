// The two pages the window shows before the server's UI can: a loading screen
// and a failure screen. They are data: URLs so they need nothing on disk and
// no preload; their buttons are links to omniplex-desktop:// that the window
// intercepts.

const STYLE = `
  :root { color-scheme: light dark; --bg: #eef2ff; --fg: #1e1b4b; --muted: #4b5563; --accent: #6366f1; }
  @media (prefers-color-scheme: dark) { :root { --bg: #0f0f1a; --fg: #e0e7ff; --muted: #9ca3af; } }
  html, body { height: 100%; margin: 0; }
  body { display: grid; place-items: center; background: var(--bg); color: var(--fg);
    font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif; }
  main { max-width: 32rem; padding: 2rem; text-align: center; }
  .mark { display: inline-grid; gap: 6px; margin-bottom: 1.5rem; }
  .mark i { display: block; width: 14px; height: 20px; border-radius: 5px; }
  .mark i:nth-child(1) { background: #6366f1; } .mark i:nth-child(2) { background: #a855f7; }
  .mark i:nth-child(3) { background: #ec4899; }
  .loading .mark i { animation: pulse 1.2s ease-in-out infinite; }
  .loading .mark i:nth-child(2) { animation-delay: .15s; } .loading .mark i:nth-child(3) { animation-delay: .3s; }
  @keyframes pulse { 50% { opacity: .35; } }
  h1 { font-size: 1.25rem; margin: 0 0 .5rem; }
  p { margin: .5rem 0; color: var(--muted); }
  code { font: 13px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; word-break: break-all; }
  .actions { margin-top: 1.5rem; display: flex; gap: .75rem; justify-content: center; flex-wrap: wrap; }
  a.button { padding: .55rem 1.1rem; border-radius: 8px; text-decoration: none; font-weight: 600;
    border: 1px solid var(--accent); color: var(--accent); }
  a.button.primary { background: var(--accent); color: #fff; }
`;

const MARK = `<div class="mark" aria-hidden="true"><i></i><i></i><i></i></div>`;

function page(title: string, bodyClass: string, body: string): string {
  const html = `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title>
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<style>${STYLE}</style></head><body class="${bodyClass}"><main>${MARK}${body}</main></body></html>`;
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`;
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

export function loadingPage(): string {
  return page("Omniplex", "loading", `<h1>Starting Omniplex…</h1>`);
}

export function errorPage(opts: { message: string; logPath: string }): string {
  return page(
    "Omniplex didn't start",
    "error",
    `<h1>Omniplex couldn't start its server</h1>
<p>${escapeHtml(opts.message)}</p>
<p>The details are in the log file:<br><code>${escapeHtml(opts.logPath)}</code></p>
<div class="actions">
  <a class="button primary" href="omniplex-desktop://retry">Try again</a>
  <a class="button" href="omniplex-desktop://show-log">Show log file</a>
</div>`,
  );
}
