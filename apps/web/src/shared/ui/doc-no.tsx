import { resolveDocOpener } from "../domain/doc-no";

/**
 * Prototype `docLink()`: a document number that opens its drawer when a feature registered an
 * opener for its prefix, plain `.doc` text otherwise, "—" when missing.
 */
export function DocNo({ value }: { value: string | null | undefined }) {
  if (!value) return <>—</>;
  const open = resolveDocOpener(value);
  if (!open) return <span className="doc">{value}</span>;
  return (
    <button type="button" className="lnk" onClick={() => open(value)}>
      {value}
    </button>
  );
}
