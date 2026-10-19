import { errorMessage } from "../../../shared/api/api-error";
import { Callout } from "../../../shared/ui/callout";

/** A section whose data could not be read: what is missing and why. */
export function LoadError({ what, error }: { what: string; error: unknown }) {
  return (
    <Callout tone="bad" role="alert">
      Không tải được {what}: {errorMessage(error)}
    </Callout>
  );
}
