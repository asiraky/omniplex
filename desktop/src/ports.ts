import net from "node:net";

// Phones bookmark the tailnet URL, and pairing cookies are scoped to the port,
// so the port has to stay put across launches. The one used last time wins,
// then the usual one, then its neighbours, and only then whatever the OS hands
// out.
export const DEFAULT_PORT = 8787;
const NEIGHBOURS = 12;

export function candidatePorts(opts: { preferred: number; lastUsed?: number }): number[] {
  const ordered = [opts.lastUsed, opts.preferred];
  for (let i = 1; i <= NEIGHBOURS; i++) ordered.push(opts.preferred + i);
  const seen = new Set<number>();
  return ordered.filter((p): p is number => {
    if (p == null || !Number.isInteger(p) || p <= 0 || p > 65535 || seen.has(p)) return false;
    seen.add(p);
    return true;
  });
}

export async function choosePort(
  candidates: number[],
  isFree: (port: number) => Promise<boolean>,
  anyFreePort: () => Promise<number>,
): Promise<number> {
  for (const port of candidates) {
    if (await isFree(port)) return port;
  }
  return anyFreePort();
}

// Loopback is where a local server already on the port would be listening:
// every Omniplex bind mode includes it.
export function isPortFree(port: number, host = "127.0.0.1"): Promise<boolean> {
  return new Promise((resolve) => {
    const srv = net.createServer();
    srv.once("error", () => resolve(false));
    srv.listen({ port, host, exclusive: true }, () => srv.close(() => resolve(true)));
  });
}

export function anyFreePort(host = "127.0.0.1"): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen({ port: 0, host, exclusive: true }, () => {
      const addr = srv.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      srv.close(() => (port ? resolve(port) : reject(new Error("no port assigned"))));
    });
  });
}
