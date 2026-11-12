import { contextBridge } from "electron";

// Tells the web UI it is in the desktop app, where it can take shortcuts a
// browser keeps for itself (Cmd/Ctrl+N, W, 1-9), and which platform it is on.
// Read as window.omniplexDesktop in web/src/lib/keybindings.ts.
contextBridge.exposeInMainWorld("omniplexDesktop", { platform: process.platform });
