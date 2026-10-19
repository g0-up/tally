import { createContext, useContext } from "react";

// SAAS-03: while the entitlements refuse posting (a read-only or closed subscription), the API
// blocks postings. The shell shows the read-only banner and screens disable their posting buttons
// (export stays available). The value is provided by the tenant provider from GET /entitlements.

export interface ReadOnlyState {
  readOnly: boolean;
  /** Explanation next to a disabled posting button. */
  reason: string | null;
}

export const READ_ONLY_REASON = "Thuê bao đang ở chế độ chỉ xem. Gia hạn để tiếp tục ghi sổ.";

export const ReadOnlyContext = createContext<ReadOnlyState>({ readOnly: false, reason: null });

export function useReadOnly(): ReadOnlyState {
  return useContext(ReadOnlyContext);
}
