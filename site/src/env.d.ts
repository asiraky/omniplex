/// <reference types="astro/client" />

interface ImportMetaEnv {
  /** Overrides the GitHub latest-release API URL, for testing the page against a fake release. */
  readonly PUBLIC_LATEST_API_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
