import { Folder } from "@/components/folder/Folder";

/**
 * An artifacts folder on a panel's page (docs/dev/ARTIFACTS.md): its records, then its files as a tree with a
 * preview; Markdown is edited in place. `dir` from ctx.artifacts.folder (it may not exist yet: the view says so and
 * fills in once the worker writes there). Give it a height: it fills its box and scrolls inside.
 */
export function ArtifactsFolder({ dir, host }: { dir: string; host?: string }) {
  return <Folder by={{ dir }} host={host} />;
}
