import { useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type Ref } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useApiClient } from "../api/api-context";
import { colorBackground, colorLabel } from "../domain/color";
import { useDebounce } from "../hooks/use-debounce";
import { FieldContext, useFieldControl } from "./field-context";
import { COMBO_MAX_RESULTS, comboEmptyText, filterComboItems, getComboPool, type ComboAdapter, type ComboItem, type ComboPool } from "./combobox-pools";

export type { ComboItem, ComboPool } from "./combobox-pools";

export const COMBO_MORE_HINT = "Gõ thêm để thu hẹp kết quả…";
export const COMBO_LOADING = "Đang tìm…";
export const COMBO_LOAD_ERROR = "Không tải được danh sách. Gõ lại để thử lại.";

const PARTY_PLACEHOLDER = "Gõ tên hoặc số điện thoại để tìm…";
const PRODUCT_PLACEHOLDER = "Gõ mã, tên hoặc mã vạch để tìm…";
const PARTY_LABEL = "Tìm và chọn đối tác";
const PRODUCT_LABEL = "Tìm và chọn hàng hóa";
const POP_MAX_HEIGHT = 320;
const POP_MIN_WIDTH = 300;
const SEARCH_DEBOUNCE_MS = 150;
const BLUR_CLOSE_MS = 120;

interface Source {
  pool?: ComboPool;
  /** Static candidates filtered on the client (small lists such as funds or units). */
  items?: readonly ComboItem[];
  /** Screen-local remote search (e.g. sellable SKUs with stock); `key` scopes its query cache. */
  source?: { key: string; search: ComboAdapter["search"] };
}

export interface ComboboxProps extends Source {
  /** "party" changes the default placeholder and accessible name. */
  kind?: "party" | "product";
  /** Selected id, or null. */
  value: string | null;
  /** Text shown for the selected value when it is not among `items` (pool-backed comboboxes). */
  valueLabel?: string;
  onChange: (item: ComboItem) => void;
  /** Extra client-side restriction on candidates. */
  filter?: (item: ComboItem) => boolean;
  placeholder?: string;
  /** Defaults to a generic name, except inside a `Field`, whose visible label names it. */
  "aria-label"?: string;
  "aria-describedby"?: string;
  id?: string;
  disabled?: boolean;
  invalid?: boolean;
  ref?: Ref<HTMLInputElement>;
}

interface Placement {
  left: number;
  top: number;
  width: number;
}

function place(input: HTMLInputElement, pop: HTMLDivElement | null): Placement {
  const r = input.getBoundingClientRect();
  const h = Math.min(POP_MAX_HEIGHT, pop?.scrollHeight || POP_MAX_HEIGHT);
  const width = Math.max(r.width, POP_MIN_WIDTH);
  const below = window.innerHeight - r.bottom > h + 8 || r.top < h + 8;
  return {
    left: Math.max(8, Math.min(r.left, window.innerWidth - width - 8)),
    top: below ? r.bottom + 2 : r.top - h - 2,
    width,
  };
}

/**
 * Searchable picker (CAT-08) with the prototype's exact keyboard model: focus opens the full
 * list and selects the text; typing filters (accent-insensitive, at most 50 options) and
 * highlights the first; ↑/↓ wrap; Enter picks; Esc closes and restores the label; Tab picks the
 * only typed match, otherwise closes and restores. The popup stays inside the field's DOM so it
 * keeps working inside modal dialogs.
 */
export function Combobox({
  pool,
  items,
  source,
  kind = "product",
  value,
  valueLabel,
  onChange,
  filter,
  placeholder,
  "aria-label": ariaLabel,
  "aria-describedby": describedBy,
  id,
  disabled,
  invalid,
  ref,
}: ComboboxProps) {
  const client = useApiClient();
  const inField = useContext(FieldContext) !== null;
  const field = useFieldControl({ describedBy, invalid });
  const listId = useId();
  const optionId = (i: number) => `${listId}-o${i}`;
  const inputRef = useRef<HTMLInputElement | null>(null);
  const popRef = useRef<HTMLDivElement | null>(null);
  const blurTimer = useRef<number | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState(false);
  const [text, setText] = useState("");
  /** Highlighted option moved by the arrow keys; null = default (current value, or first match while typing). */
  const [cursor, setCursor] = useState<number | null>(null);
  const [pos, setPos] = useState<Placement | null>(null);
  const [picked, setPicked] = useState<ComboItem | null>(null);

  const query = typed ? text : "";
  const debounced = useDebounce(query, SEARCH_DEBOUNCE_MS);
  const adapter = source ?? (pool ? getComboPool(pool) : undefined);
  const isRemote = pool !== undefined || source !== undefined;

  const remote = useQuery({
    queryKey: ["combobox", source?.key ?? pool ?? "static", debounced],
    queryFn: ({ signal }) => {
      if (!adapter) throw new Error(`No search adapter registered for pool "${pool}"`);
      return adapter.search({ client, query: debounced, signal });
    },
    enabled: open && isRemote,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
    meta: { silent: true },
  });

  const results = useMemo(() => {
    const restrict = (list: readonly ComboItem[]) => (filter ? list.filter(filter) : list);
    if (items) return filterComboItems(restrict(items), query);
    return restrict(remote.data ?? []).slice(0, COMBO_MAX_RESULTS);
  }, [items, filter, query, remote.data]);
  /** Results are for the text currently in the field (no pending debounce or request). */
  const fresh = items !== undefined || (debounced === query && !remote.isFetching && !remote.isPlaceholderData);

  const selectedLabel = useMemo(() => {
    if (value === null) return "";
    const fromItems = items?.find((x) => x.id === value) ?? (picked?.id === value ? picked : undefined);
    if (fromItems) return fromItems.inputLabel ?? fromItems.name;
    return valueLabel ?? "";
  }, [value, items, picked, valueLabel]);

  const shown = open ? text : selectedLabel;

  const n = results.length;
  const defaultIdx = typed ? 0 : Math.max(0, results.findIndex((x) => x.id === value));
  const idx = n === 0 ? -1 : Math.min(cursor ?? defaultIdx, n - 1);

  const close = useCallback(() => {
    setOpen(false);
    setTyped(false);
    setCursor(null);
  }, []);

  const choose = (item: ComboItem | undefined) => {
    if (!item) return;
    setPicked(item);
    close();
    onChange(item);
  };

  useLayoutEffect(() => {
    if (!open || !inputRef.current) return;
    const update = () => inputRef.current && setPos(place(inputRef.current, popRef.current));
    update();
    window.addEventListener("scroll", update, true);
    window.addEventListener("resize", update);
    return () => {
      window.removeEventListener("scroll", update, true);
      window.removeEventListener("resize", update);
    };
  }, [open, n]);

  useEffect(() => {
    if (!open || idx < 0) return;
    document.getElementById(optionId(idx))?.scrollIntoView({ block: "nearest" });
    // optionId is derived from a stable useId value.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, idx]);

  // A field that unmounts mid-blur must not touch the document once it is gone.
  useEffect(() => () => window.clearTimeout(blurTimer.current), []);

  // A press anywhere outside the field and its popup closes it and restores the label.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node | null;
      const root = inputRef.current?.parentElement;
      if (t && root && !root.contains(t)) close();
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open, close]);

  const openList = () => {
    setText(selectedLabel);
    setTyped(false);
    setCursor(null);
    setOpen(true);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!open) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        openList();
      }
      return;
    }
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (n) setCursor((idx + 1) % n);
        break;
      case "ArrowUp":
        e.preventDefault();
        if (n) setCursor((idx - 1 + n) % n);
        break;
      case "Enter":
        e.preventDefault();
        e.stopPropagation();
        if (idx >= 0) choose(results[idx]);
        break;
      case "Escape":
        e.preventDefault();
        e.stopPropagation();
        close();
        break;
      case "Tab":
        if (typed && fresh && n === 1) choose(results[0]);
        else close();
        break;
    }
  };

  const setRefs = (el: HTMLInputElement | null) => {
    inputRef.current = el;
    if (typeof ref === "function") ref(el);
    else if (ref) ref.current = el;
  };

  const isParty = kind === "party";
  const defaultLabel = isParty ? PARTY_LABEL : PRODUCT_LABEL;
  const loading = isRemote && remote.data === undefined && remote.isFetching;
  const failed = isRemote && remote.isError;

  return (
    <div className="cbx">
      <input
        ref={setRefs}
        id={id}
        className="in cbx-in"
        type="text"
        autoComplete="off"
        spellCheck={false}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open && n ? listId : undefined}
        aria-activedescendant={open && idx >= 0 ? optionId(idx) : undefined}
        aria-label={ariaLabel ?? (inField ? undefined : defaultLabel)}
        {...field}
        placeholder={placeholder ?? (isParty ? PARTY_PLACEHOLDER : PRODUCT_PLACEHOLDER)}
        disabled={disabled}
        value={shown}
        onFocus={(e) => {
          openList();
          const el = e.currentTarget;
          const label = el.value;
          // Select the label for overtyping, unless typing (or a barcode scan) already started.
          requestAnimationFrame(() => {
            if (document.activeElement === el && el.value === label) el.select();
          });
        }}
        onChange={(e) => {
          setText(e.target.value);
          setTyped(true);
          setCursor(null);
          if (!open) setOpen(true);
        }}
        onBlur={() => {
          window.clearTimeout(blurTimer.current);
          blurTimer.current = window.setTimeout(() => {
            if (document.activeElement !== inputRef.current) close();
          }, BLUR_CLOSE_MS);
        }}
        onKeyDown={onKeyDown}
      />
      <span className="cbx-caret" aria-hidden="true" onClick={() => inputRef.current?.focus()}>
        ▾
      </span>
      {open ? (
        <div
          ref={popRef}
          className="cbx-pop"
          style={pos ? { left: pos.left, top: pos.top, width: pos.width } : undefined}
          onMouseDown={(e) => e.preventDefault()}
        >
          {n ? (
            <div role="listbox" id={listId} aria-label={ariaLabel ?? defaultLabel}>
              {results.map((x, i) => (
                <button
                  key={x.id}
                  id={optionId(i)}
                  type="button"
                  className="cbx-opt"
                  role="option"
                  tabIndex={-1}
                  aria-selected={i === idx}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    choose(x);
                  }}
                >
                  <b>
                    {x.name}
                    {x.colors?.length
                      ? x.colors.map((c) => <i key={c} className="cbx-dot" style={{ background: colorBackground(c) }} title={colorLabel(c)} />)
                      : null}
                  </b>
                  {x.detail ? <small>{x.detail}</small> : null}
                </button>
              ))}
            </div>
          ) : null}
          {failed ? (
            <div className="cbx-empty" role="alert">
              {COMBO_LOAD_ERROR}
            </div>
          ) : loading ? (
            <div className="cbx-empty">{COMBO_LOADING}</div>
          ) : n === 0 ? (
            <div className="cbx-empty">{comboEmptyText(text)}</div>
          ) : n === COMBO_MAX_RESULTS ? (
            <div className="cbx-empty">{COMBO_MORE_HINT}</div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
