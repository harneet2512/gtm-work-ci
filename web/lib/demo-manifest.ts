// Which demo manifest the control plane opens. The core has no manifest list endpoint, so the default is configuration
// (a server-side environment value): the page opens on it directly and nobody types a uuid. A manifest in the URL wins;
// an invalid one (in the URL or in the configuration) is reported, never silently replaced or ignored.
import { UUID } from "./uuid";

export type ManifestSource = "url" | "default" | "invalid" | "invalid_config" | "none";

export interface ResolvedManifest {
  manifestId: string | null;
  source: ManifestSource;
}

export function demoManifestFromEnv(env: Readonly<Record<string, string | undefined>>): string | null {
  const id = env.GHOST_DEMO_MANIFEST_ID?.trim().toLowerCase();
  return id && UUID.test(id) ? id : null;
}

export function resolveManifest(rawUrlManifest: string | undefined, env: Readonly<Record<string, string | undefined>>): ResolvedManifest {
  const fromUrl = rawUrlManifest?.trim().toLowerCase();
  if (fromUrl) return UUID.test(fromUrl) ? { manifestId: fromUrl, source: "url" } : { manifestId: null, source: "invalid" };
  const configured = demoManifestFromEnv(env);
  if (configured) return { manifestId: configured, source: "default" };
  // A configured but malformed id is a setup mistake: say so rather than behave as if nothing was configured.
  return { manifestId: null, source: env.GHOST_DEMO_MANIFEST_ID?.trim() ? "invalid_config" : "none" };
}

/** The plain-words notice for a manifest that cannot be used; null when there is nothing to say. */
export function manifestNotice(source: ManifestSource): string | null {
  if (source === "invalid") return "That account id is not a valid id.";
  if (source === "invalid_config") return "The demo account configured for this app is not a valid id, so none was opened. Enter the account's id.";
  return null;
}
