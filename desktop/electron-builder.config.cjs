// electron-builder configuration for the Omniplex desktop app.
//
// Signing is driven entirely by the environment, so the same config builds an
// unsigned app on a laptop or a CI job without secrets. See README.md for the
// variables and for where the server binary has to be staged first.

const fs = require("node:fs");
const path = require("node:path");

const env = process.env;
const macSigning = Boolean(env.CSC_LINK || env.CSC_NAME);
const azure =
  env.AZURE_SIGNING_ENDPOINT && env.AZURE_SIGNING_CERT_PROFILE && env.AZURE_SIGNING_ACCOUNT
    ? {
        endpoint: env.AZURE_SIGNING_ENDPOINT,
        certificateProfileName: env.AZURE_SIGNING_CERT_PROFILE,
        codeSigningAccountName: env.AZURE_SIGNING_ACCOUNT,
        publisherName: env.AZURE_SIGNING_PUBLISHER,
      }
    : undefined;

if (azure && !azure.publisherName) {
  // The updater refuses an update whose signer is not this exact name.
  throw new Error("AZURE_SIGNING_PUBLISHER must be set with the other AZURE_SIGNING_* variables");
}

/** @type {import("electron-builder").Configuration} */
module.exports = {
  appId: "com.omniplex.desktop",
  productName: "Omniplex",
  // npm hoists electron to the workspace root, where electron-builder does not
  // look; read the installed version instead.
  electronVersion: require("electron/package.json").version,
  copyright: "Copyright © Omniplex contributors",
  directories: { output: "release", buildResources: "build" },
  files: ["dist/**/*", "package.json"],
  // The main process is one bundled file with no runtime dependencies.
  npmRebuild: false,
  artifactName: "${productName}-${version}-${os}-${arch}.${ext}",
  // Refuse to publish an unsigned release when the workflow says it must sign.
  forceCodeSigning: env.OMNIPLEX_REQUIRE_SIGNING === "1",

  extraResources: [
    // Staged per target before packaging: resources/bin/<os>-<arch>/omniplex[.exe],
    // where <os> is mac, win or linux and <arch> is arm64 or x64.
    { from: "resources/bin/${os}-${arch}", to: "bin", filter: ["omniplex", "omniplex.exe"] },
    { from: "resources/icons", to: "icons" },
  ],

  publish: [{ provider: "github", owner: "asiraky", repo: "omniplex", releaseType: "draft" }],

  mac: {
    category: "public.app-category.developer-tools",
    icon: "build/icon.png",
    target: ["dmg", "zip"],
    hardenedRuntime: true,
    gatekeeperAssess: false,
    entitlements: "build/entitlements.mac.plist",
    entitlementsInherit: "build/entitlements.mac.plist",
    // Signed explicitly with the entitlements above; the server spawns
    // harnesses and extracts a JIT-using sidecar under the hardened runtime.
    binaries: ["Contents/Resources/bin/omniplex"],
    // Without a certificate, sign ad hoc so an Apple Silicon build still runs.
    ...(macSigning ? {} : { identity: "-" }),
    // Notarizes only when APPLE_API_KEY, APPLE_API_KEY_ID and APPLE_API_ISSUER
    // are set.
    notarize: macSigning,
  },
  win: {
    icon: "build/icon-win.png",
    target: ["nsis"],
    ...(azure ? { azureSignOptions: azure } : {}),
  },
  nsis: {
    oneClick: true,
    perMachine: false,
    createDesktopShortcut: true,
    createStartMenuShortcut: true,
    shortcutName: "Omniplex",
    runAfterFinish: true,
    deleteAppDataOnUninstall: false,
  },

  linux: {
    icon: "build/icon-win.png",
    target: ["AppImage"],
    category: "Development",
    executableName: "omniplex-desktop",
  },

  // A package without the server is a broken app; fail the build instead.
  afterPack: async (context) => {
    const exe = context.electronPlatformName === "win32" ? "omniplex.exe" : "omniplex";
    const resources =
      context.electronPlatformName === "darwin"
        ? path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, "Contents", "Resources")
        : path.join(context.appOutDir, "resources");
    const binary = path.join(resources, "bin", exe);
    if (!fs.existsSync(binary)) {
      throw new Error(
        `The Omniplex server is missing from the package (${binary}). ` +
          "Stage it at desktop/resources/bin/<os>-<arch>/ first; see desktop/README.md.",
      );
    }
  },
};
