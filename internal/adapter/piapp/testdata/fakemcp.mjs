// A stdio MCP server for mcpbridge.test.mjs. Tools: echo (returns its text,
// plus FAKE_MCP_TOKEN from env), boom (an isError result), wait (never
// answers; on notifications/cancelled it writes the cancelled id to
// FAKE_MCP_CANCELLED and still sends no reply).
import { writeFileSync } from "node:fs";

let buf = "";
const send = (m) => process.stdout.write(JSON.stringify(m) + "\n");

process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buf += chunk;
  let nl;
  while ((nl = buf.indexOf("\n")) >= 0) {
    const m = JSON.parse(buf.slice(0, nl));
    buf = buf.slice(nl + 1);
    handle(m);
  }
});

function handle(m) {
  if (m.method === "notifications/cancelled") {
    writeFileSync(process.env.FAKE_MCP_CANCELLED, JSON.stringify(m.params.requestId));
    return;
  }
  if (m.id === undefined) return;
  switch (m.method) {
    case "initialize":
      return send({
        jsonrpc: "2.0",
        id: m.id,
        result: { protocolVersion: m.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "fake" }, instructions: "Use mcp__fake__echo to echo." },
      });
    case "tools/list":
      return send({
        jsonrpc: "2.0",
        id: m.id,
        result: {
          tools: [
            { name: "echo", description: "Echo text.", inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] } },
            { name: "boom", description: "Always fails.", inputSchema: { type: "object" } },
            { name: "wait", description: "Never answers." },
          ],
        },
      });
    case "tools/call": {
      const { name, arguments: args } = m.params;
      if (name === "echo") {
        return send({ jsonrpc: "2.0", id: m.id, result: { content: [{ type: "text", text: `${args.text} ${process.env.FAKE_MCP_TOKEN ?? ""}`.trim() }] } });
      }
      if (name === "boom") {
        return send({ jsonrpc: "2.0", id: m.id, result: { isError: true, content: [{ type: "text", text: "it broke" }] } });
      }
      return; // wait
    }
    default:
      send({ jsonrpc: "2.0", id: m.id, error: { code: -32601, message: "nope" } });
  }
}
