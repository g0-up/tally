import { errorMessage } from "../../../shared/api/api-error";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";

/** A pricing read that failed: the translated reason and a retry, so it never reads as an empty table. */
export function LoadError({ what, error, onRetry }: { what: string; error: unknown; onRetry: () => void }) {
  return (
    <Callout tone="bad" role="alert">
      Không tải được {what}: {errorMessage(error)}{" "}
      <Button size="sm" onClick={onRetry}>
        Thử lại
      </Button>
    </Callout>
  );
}
