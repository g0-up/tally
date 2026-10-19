import { useState } from "react";
import { useSearchParams } from "react-router";

/**
 * The document a screen shows in its drawer: one opened by click, or the one a `?doc=` link names
 * (resolved against the rows the screen knows: its loaded pages, plus the server's answer for that
 * number where the list filters by it). A later `?doc=` link takes over from an earlier click.
 */
export function useDocParam<T extends { id: string; doc_no: string }>(rows: readonly T[], loaded: boolean) {
  const [params, setParams] = useSearchParams();
  const [clicked, setClicked] = useState<{ id: string; doc: string | null } | null>(null);
  const doc = params.get("doc");
  const clickedId = clicked !== null && clicked.doc === doc ? clicked.id : null;
  const linked = clickedId === null && doc !== null ? rows.find((r) => r.doc_no === doc) : undefined;

  const dropDoc = () =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        next.delete("doc");
        return next;
      },
      { replace: true },
    );
  return {
    id: clickedId ?? linked?.id ?? null,
    /** The linked number when the loaded rows do not carry it. */
    missing: clickedId === null && doc !== null && loaded && !linked ? doc : null,
    open: (id: string) => {
      setClicked({ id, doc: null });
      if (doc !== null) dropDoc();
    },
    close: () => {
      setClicked(null);
      if (doc !== null) dropDoc();
    },
  };
}
