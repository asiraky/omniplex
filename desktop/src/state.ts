import fs from "node:fs";
import path from "node:path";

// The little the app remembers between launches, in userData.
export interface DesktopState {
  // Set once the login item has been registered on first run, so turning
  // "Open at login" off in the tray sticks.
  loginItemConfigured?: boolean;
  // Set once the setup screen has been shown.
  setupShown?: boolean;
  lastPort?: number;
}

export class StateFile {
  private data: DesktopState;

  constructor(private readonly file: string) {
    this.data = StateFile.read(file);
  }

  static read(file: string): DesktopState {
    try {
      const parsed: unknown = JSON.parse(fs.readFileSync(file, "utf8"));
      return typeof parsed === "object" && parsed !== null ? (parsed as DesktopState) : {};
    } catch {
      return {};
    }
  }

  get(): Readonly<DesktopState> {
    return this.data;
  }

  update(patch: Partial<DesktopState>): void {
    this.data = { ...this.data, ...patch };
    fs.mkdirSync(path.dirname(this.file), { recursive: true });
    fs.writeFileSync(this.file, JSON.stringify(this.data, null, 2));
  }
}
