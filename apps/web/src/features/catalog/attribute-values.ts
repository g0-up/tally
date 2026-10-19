import { useMemo } from "react";
import { useAttributeSet, useAttributes } from "./api/queries";
import type { Attribute } from "./schemas";

// Form values of product attributes and their request shape. The form keeps every value as the
// control holds it (numbers as typed text); the API wants attribute code → typed JSON value.

export type AttributeValue = string | boolean | string[] | null;
export type AttributeValues = Readonly<Record<string, AttributeValue>>;

const NUMBER_TEXT = /^-?\d+(?:[.,]\d+)?$/;

function isEmpty(v: AttributeValue | undefined): boolean {
  return v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0);
}

/**
 * Request value per attribute code. Empty values are left out (the server reports a missing
 * required one); a number that does not parse is sent as typed so the server names the field.
 */
export function attributeRequest(values: AttributeValues, attributes: readonly Attribute[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const a of attributes) {
    const v = values[a.code];
    if (isEmpty(v)) continue;
    if (a.data_type === "number" && typeof v === "string") {
      const text = v.trim().replace(",", ".");
      out[a.code] = NUMBER_TEXT.test(text) ? Number(text) : v;
    } else if (a.data_type === "text" && typeof v === "string") {
      out[a.code] = v.trim();
    } else {
      out[a.code] = v;
    }
  }
  return out;
}

/** Required attributes of the set that have no value yet, by code. */
export function missingRequired(values: AttributeValues, required: readonly string[]): string[] {
  return required.filter((code) => {
    const v = values[code];
    return isEmpty(v) || (typeof v === "string" && v.trim() === "");
  });
}

export interface SetAttribute {
  attribute: Attribute;
  required: boolean;
}

/** Active attributes of an attribute set in set order; empty while loading or without a set. */
export function useSetAttributes(attributeSetId: string | null): SetAttribute[] {
  const set = useAttributeSet(attributeSetId);
  const attributes = useAttributes(attributeSetId !== null);
  return useMemo(() => {
    if (attributeSetId === null || !set.data || !attributes.data) return [];
    const byId = new Map(attributes.data.map((a) => [a.id, a]));
    return [...set.data.items]
      .sort((a, b) => a.sort_order - b.sort_order)
      .flatMap((it) => {
        const a = byId.get(it.attribute_id);
        return a && a.status === "active" ? [{ attribute: a, required: it.is_required }] : [];
      });
  }, [attributeSetId, set.data, attributes.data]);
}
