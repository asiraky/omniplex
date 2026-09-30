import net from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { anyFreePort, candidatePorts, choosePort, isPortFree } from "./ports";

describe("candidatePorts", () => {
  it("tries the last port first, then the preferred one and its neighbours", () => {
    const ports = candidatePorts({ preferred: 8787, lastUsed: 8790 });
    expect(ports.slice(0, 3)).toEqual([8790, 8787, 8788]);
    expect(ports.filter((p) => p === 8790)).toHaveLength(1);
  });

  it("ignores a missing or nonsense last port", () => {
    expect(candidatePorts({ preferred: 8787 })[0]).toBe(8787);
    expect(candidatePorts({ preferred: 8787, lastUsed: 0 })[0]).toBe(8787);
    expect(candidatePorts({ preferred: 8787, lastUsed: 70000 })[0]).toBe(8787);
  });
});

describe("choosePort", () => {
  it("takes the first free candidate", async () => {
    const taken = new Set([8787, 8788]);
    const port = await choosePort([8787, 8788, 8789], async (p) => !taken.has(p), async () => 1);
    expect(port).toBe(8789);
  });

  it("asks the OS when every candidate is taken", async () => {
    const port = await choosePort([8787, 8788], async () => false, async () => 54321);
    expect(port).toBe(54321);
  });
});

describe("isPortFree", () => {
  const servers: net.Server[] = [];
  afterEach(async () => {
    await Promise.all(servers.splice(0).map((s) => new Promise((r) => s.close(r))));
  });

  it("sees a port something is listening on as taken", async () => {
    const port = await anyFreePort();
    expect(await isPortFree(port)).toBe(true);
    const srv = net.createServer();
    servers.push(srv);
    await new Promise<void>((r) => srv.listen(port, "127.0.0.1", r));
    expect(await isPortFree(port)).toBe(false);
  });
});
