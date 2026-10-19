import { useCallback } from "react";
import { useSearchParams } from "react-router";

/**
 * Filter state kept in the URL query so a filtered Kho screen can be linked and survives reload.
 * `set` replaces the history entry; an empty or null value removes the parameter.
 */
export function useUrlFilters() {
  const [params, setParams] = useSearchParams();
  const set = useCallback(
    (patch: Readonly<Record<string, string | null>>) =>
      setParams(
        (cur) => {
          const next = new URLSearchParams(cur);
          for (const [k, v] of Object.entries(patch)) {
            if (v === null || v === "") next.delete(k);
            else next.set(k, v);
          }
          return next;
        },
        { replace: true },
      ),
    [setParams],
  );
  return { get: (key: string) => params.get(key), set };
}
