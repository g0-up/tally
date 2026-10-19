import type { ReactNode } from "react";

/** Prototype dashboard band: `section.band` with an uppercase heading and a short note. */
export function Band({ title, note, children }: { title: ReactNode; note?: ReactNode; children: ReactNode }) {
  return (
    <section className="band">
      <div className="band-h">
        <h2>{title}</h2>
        {note !== undefined ? <span>{note}</span> : null}
      </div>
      {children}
    </section>
  );
}
