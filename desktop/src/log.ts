import fs from "node:fs";
import path from "node:path";

const MAX_BYTES = 10 * 1024 * 1024;

// One file for everything: the server's stdout and stderr go straight into it
// through an inherited descriptor, and the desktop app's own lines are
// appended beside them. That is the file to ask a user for.
export class LogFile {
  readonly path: string;
  readonly fd: number;

  constructor(
    dir: string,
    private readonly echo: boolean,
    name = "omniplex.log",
  ) {
    fs.mkdirSync(dir, { recursive: true });
    this.path = path.join(dir, name);
    try {
      if (fs.statSync(this.path).size > MAX_BYTES) fs.renameSync(this.path, `${this.path}.1`);
    } catch {
      // No log yet.
    }
    this.fd = fs.openSync(this.path, "a");
  }

  line(message: string): void {
    const text = `${new Date().toISOString()} [desktop] ${message}\n`;
    try {
      fs.writeSync(this.fd, text);
    } catch {
      // Logging must never take the app down.
    }
    if (this.echo) process.stdout.write(text);
  }
}
