import { useSearchParams } from "react-router";

/** One query-string value (`?party=`, `?doc=`) with setters that replace the history entry. */
export function useSearchParam(name: string) {
  const [params, setParams] = useSearchParams();
  const write = (value: string | null) =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        if (value === null) next.delete(name);
        else next.set(name, value);
        return next;
      },
      { replace: true },
    );
  return { value: params.get(name), set: (value: string) => write(value), drop: () => write(null) };
}
