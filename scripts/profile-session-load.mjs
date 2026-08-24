// Repeatable browser-side session-load probe with no npm dependency.
// Start Chrome with --remote-debugging-port and a fresh --user-data-dir, then:
// node scripts/profile-session-load.mjs <port> <app-url> <session-title-prefix> [width] [cpu] [latency-ms] [down-kib-s]

const [port, appURL, titlePrefix, widthArg = "1440", cpuArg = "1", latencyArg = "0", downArg = "0"] = process.argv.slice(2);
if (!port || !appURL || !titlePrefix) throw new Error("missing port, app URL, or session title prefix");

const pages = await fetch(`http://127.0.0.1:${port}/json/list`).then((r) => r.json());
const page = pages.find((p) => p.type === "page");
if (!page) throw new Error("Chrome has no debuggable page");

const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  ws.addEventListener("open", resolve, { once: true });
  ws.addEventListener("error", reject, { once: true });
});

let nextID = 1;
const pending = new Map();
const responses = new Map();
const sessionRequests = new Map();
const wire = { httpBytes: 0, wsSnapshotBytes: 0 };

ws.addEventListener("message", (message) => {
  const event = JSON.parse(message.data);
  if (event.id) {
    const waiter = pending.get(event.id);
    pending.delete(event.id);
    if (event.error) waiter?.reject(new Error(event.error.message));
    else waiter?.resolve(event.result);
    return;
  }
  if (event.method === "Network.responseReceived") {
    responses.set(event.params.requestId, event.params.response.url);
  } else if (event.method === "Network.loadingFinished") {
    const url = responses.get(event.params.requestId);
    if (url?.includes("/api/sessions/")) {
      sessionRequests.set(url, event.params.encodedDataLength);
      wire.httpBytes += event.params.encodedDataLength;
    }
  } else if (event.method === "Network.webSocketFrameReceived") {
    const payload = event.params.response.payloadData;
    if (payload.includes('"type":"snapshot"')) wire.wsSnapshotBytes += Buffer.byteLength(payload);
  }
});

function call(method, params = {}) {
  const id = nextID++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

async function evaluate(expression, awaitPromise = false) {
  const result = await call("Runtime.evaluate", { expression, awaitPromise, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text);
  return result.result.value;
}

async function until(check, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await check();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("timed out waiting for browser state");
}

await call("Page.enable");
await call("Runtime.enable");
await call("Network.enable");
await call("Page.addScriptToEvaluateOnNewDocument", {
  source: "localStorage.clear(); sessionStorage.clear();",
});
await call("Emulation.setDeviceMetricsOverride", {
  width: Number(widthArg),
  height: Number(widthArg) <= 500 ? 844 : 1000,
  deviceScaleFactor: 1,
  mobile: Number(widthArg) <= 500,
});
await call("Emulation.setCPUThrottlingRate", { rate: Number(cpuArg) });
if (Number(latencyArg) > 0 || Number(downArg) > 0) {
  await call("Network.emulateNetworkConditions", {
    offline: false,
    latency: Number(latencyArg),
    downloadThroughput: Number(downArg) * 1024,
    uploadThroughput: Number(downArg) * 1024,
    connectionType: "cellular3g",
  });
}

await call("Page.navigate", { url: appURL });
await until(() => evaluate(`document.readyState === "complete" && [...document.querySelectorAll("button")].some((b) => b.innerText.includes(${JSON.stringify(titlePrefix)}))`));

responses.clear();
sessionRequests.clear();
wire.httpBytes = 0;
wire.wsSnapshotBytes = 0;

const clicked = await evaluate(`(() => {
  const button = [...document.querySelectorAll("button")].find((b) => b.innerText.includes(${JSON.stringify(titlePrefix)}));
  if (!button) return false;
  performance.clearMeasures("omniplex.session_snapshot");
  window.__omniplexProfileClick = performance.now();
  button.click();
  return true;
})()`);
if (!clicked) throw new Error("session button disappeared before click");

await until(() => evaluate(`!!document.querySelector("textarea") && !document.body.innerText.includes("Attaching…")`), 30_000);
const result = await evaluate(`new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve({
  clickToPaintMs: performance.now() - window.__omniplexProfileClick,
  snapshotMs: performance.getEntriesByName("omniplex.session_snapshot").at(-1)?.duration ?? null,
}))))`, true);

await new Promise((resolve) => setTimeout(resolve, 100));
console.log(JSON.stringify({
  ...result,
  width: Number(widthArg),
  cpuRate: Number(cpuArg),
  latencyMs: Number(latencyArg),
  downKiBps: Number(downArg),
  httpSnapshotBytes: wire.httpBytes,
  wsSnapshotBytes: wire.wsSnapshotBytes,
  sessionRequests: Object.fromEntries(sessionRequests),
}));
ws.close();
