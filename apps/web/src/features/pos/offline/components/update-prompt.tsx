import { useState } from "react";
import { errorMessage } from "../../../../shared/api/api-error";
import { Button, ButtonRow } from "../../../../shared/ui/button";
import { Callout } from "../../../../shared/ui/callout";
import { UPDATE_MESSAGE, dismissUpdate, useAppUpdate } from "../app-update";

/**
 * Bottom notice while a new version waits; stays until the user reloads or puts it off. The status
 * region is always mounted and only its content changes, so the notice is read when it appears.
 */
export function UpdatePrompt() {
  const apply = useAppUpdate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reload = async () => {
    if (apply === null) return;
    setBusy(true);
    setError(null);
    try {
      await apply();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  return <div role="status">{apply === null ? null : <UpdateNotice busy={busy} error={error} onReload={() => void reload()} />}</div>;
}

function UpdateNotice({ busy, error, onReload }: { busy: boolean; error: string | null; onReload: () => void }) {
  return (
    <Callout className="update-prompt">
      {UPDATE_MESSAGE}
      {error ? <> Chưa tải lại được: {error}</> : null}
      <ButtonRow style={{ marginTop: 8 }}>
        <Button variant="navy" size="sm" pending={busy} pendingLabel="Đang tải lại…" onClick={onReload}>
          Tải lại
        </Button>
        <Button variant="line" size="sm" disabled={busy} onClick={dismissUpdate}>
          Để sau
        </Button>
      </ButtonRow>
    </Callout>
  );
}
