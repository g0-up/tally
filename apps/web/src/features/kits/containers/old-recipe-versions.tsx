import { useState } from "react";
import { fd } from "../../../shared/lib/format";
import { LinkButton } from "../../../shared/ui/link-button";
import { useRecipeVersions } from "../api/queries";

export interface OldRecipeVersionsProps {
  kitId: string;
  /** Superseded version numbers, oldest first. */
  versions: readonly number[];
}

/** "Phiên bản cũ" of a card: the superseded versions with their validity, loaded when asked for. */
export function OldRecipeVersions({ kitId, versions }: OldRecipeVersionsProps) {
  const [open, setOpen] = useState(false);
  if (versions.length === 0) return null;
  return (
    <p className="hint" style={{ marginTop: 10 }}>
      {open ? (
        <VersionLine kitId={kitId} versions={versions} />
      ) : (
        <LinkButton onClick={() => setOpen(true)}>Phiên bản cũ ({versions.length})</LinkButton>
      )}
    </p>
  );
}

function VersionLine({ kitId, versions }: OldRecipeVersionsProps) {
  const { recipes, pending, failed } = useRecipeVersions(kitId, versions);
  if (failed) return <>Phiên bản cũ: không tải được.</>;
  if (pending) return <>Phiên bản cũ: đang tải…</>;
  return <>Phiên bản cũ: {recipes.map((o) => `v${o.version_no} (${fd(o.valid_from)} – ${fd(o.valid_to)})`).join(", ")}</>;
}
