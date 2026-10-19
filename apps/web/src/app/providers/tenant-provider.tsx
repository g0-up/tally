import { useMemo, type ReactNode } from "react";
import { useEntitlements } from "../../shared/api/platform-queries";
import { OneStoreContext } from "../../shared/hooks/use-one-store";
import { READ_ONLY_REASON, ReadOnlyContext } from "../../shared/hooks/use-read-only";

/**
 * Store-wide facts screens read without fetching: one store/one warehouse (always true in this
 * release, ORG-03) and whether the subscription blocks posting (SAAS-03). Posting rights come from
 * GET /entitlements (`can_post`): a store in grace still posts, a read-only or closed one does not.
 * Until the answer arrives, or if it cannot be read, nothing is disabled and the API decides.
 */
export function TenantProvider({ children }: { children: ReactNode }) {
  const entitlements = useEntitlements();
  const readOnly = entitlements.data?.can_post === false;
  const value = useMemo(() => ({ readOnly, reason: readOnly ? READ_ONLY_REASON : null }), [readOnly]);
  return (
    <OneStoreContext.Provider value={true}>
      <ReadOnlyContext.Provider value={value}>{children}</ReadOnlyContext.Provider>
    </OneStoreContext.Provider>
  );
}
