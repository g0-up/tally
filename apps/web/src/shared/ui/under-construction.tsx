import { PageHeader } from "./page-header";

export const UNDER_CONSTRUCTION = "Đang xây dựng.";

/** Prototype fallback for a menu view without a screen yet: page header + card "Đang xây dựng.". */
export function UnderConstruction() {
  return (
    <>
      <PageHeader />
      <div className="card">{UNDER_CONSTRUCTION}</div>
    </>
  );
}
