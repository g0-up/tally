import type { ReactNode } from "react";
import { Checkbox } from "../../shared/ui/checkbox";
import { Combobox } from "../../shared/ui/combobox";
import { DatePicker } from "../../shared/ui/date-picker";
import { Field } from "../../shared/ui/field";
import { FormGrid } from "../../shared/ui/form-grid";
import { Input } from "../../shared/ui/input";
import type { AttributeValue, AttributeValues, SetAttribute } from "./attribute-values";
import type { Attribute, AttributeDataType } from "./schemas";

// Product attributes render through one registry keyed by data type, so a new type is one entry
// here plus its validator on the server.

interface AttributeControlProps {
  attribute: Attribute;
  value: AttributeValue | undefined;
  onChange: (value: AttributeValue) => void;
  invalid: boolean;
}

type AttributeControl = (props: AttributeControlProps) => ReactNode;

function TextControl({ value, onChange, invalid }: AttributeControlProps) {
  return <Input value={typeof value === "string" ? value : ""} invalid={invalid} onChange={(e) => onChange(e.target.value)} />;
}

function NumberControl({ value, onChange, invalid }: AttributeControlProps) {
  return <Input inputMode="decimal" value={typeof value === "string" ? value : ""} invalid={invalid} onChange={(e) => onChange(e.target.value)} />;
}

// The field label already wraps the control, so the box is a bare input (no nested label).
function BooleanControl({ value, onChange }: AttributeControlProps) {
  return <input type="checkbox" checked={value === true} onChange={(e) => onChange(e.target.checked)} />;
}

function DateControl({ attribute, value, onChange, invalid }: AttributeControlProps) {
  return <DatePicker value={typeof value === "string" ? value : null} onChange={onChange} invalid={invalid} aria-label={attribute.name} />;
}

function SelectControl({ attribute, value, onChange, invalid }: AttributeControlProps) {
  const items = attribute.options.filter((o) => o.active || o.id === value).map((o) => ({ id: o.id, name: o.label }));
  return <Combobox items={items} value={typeof value === "string" ? value : null} onChange={(item) => onChange(item.id)} invalid={invalid} aria-label={attribute.name} placeholder="Chọn…" />;
}

function MultiselectControl({ attribute, value, onChange }: AttributeControlProps) {
  const picked = Array.isArray(value) ? value : [];
  return (
    <div role="group" aria-label={attribute.name}>
      {attribute.options
        .filter((o) => o.active || picked.includes(o.id))
        .map((o) => (
          <Checkbox
            key={o.id}
            label={o.label}
            checked={picked.includes(o.id)}
            onChange={(e) => onChange(e.target.checked ? [...picked, o.id] : picked.filter((x) => x !== o.id))}
          />
        ))}
    </div>
  );
}

export const ATTRIBUTE_FIELD_REGISTRY: Readonly<Record<AttributeDataType, AttributeControl>> = {
  text: TextControl,
  number: NumberControl,
  boolean: BooleanControl,
  date: DateControl,
  select: SelectControl,
  multiselect: MultiselectControl,
};

export interface AttributeFieldsProps {
  items: readonly SetAttribute[];
  values: AttributeValues;
  onChange: (code: string, value: AttributeValue) => void;
  /** Messages keyed by attribute code (the API `attributes.{code}` fields, already in Vietnamese). */
  errors: Readonly<Record<string, string>>;
}

/** Prototype-style fields for the product's attributes; nothing when the set has none. */
export function AttributeFields({ items, values, onChange, errors }: AttributeFieldsProps) {
  if (items.length === 0) return null;
  return (
    <>
      <h3 className="sec-h">Thuộc tính</h3>
      <FormGrid>
        {items.map(({ attribute, required }) => {
          const Control = ATTRIBUTE_FIELD_REGISTRY[attribute.data_type];
          const error = errors[attribute.code];
          const label = `${attribute.name}${attribute.unit_label ? ` (${attribute.unit_label})` : ""}${required ? " *" : ""}`;
          const control = <Control attribute={attribute} value={values[attribute.code]} onChange={(v) => onChange(attribute.code, v)} invalid={error !== undefined} />;
          if (attribute.data_type === "multiselect") {
            // A group of labelled checkboxes cannot sit inside the field's <label>.
            return (
              <div key={attribute.id} className="f">
                <span>{label}</span>
                {control}
                {error === undefined ? null : (
                  <span className="err" role="alert">
                    {error}
                  </span>
                )}
              </div>
            );
          }
          return (
            <Field key={attribute.id} label={label} error={error}>
              {control}
            </Field>
          );
        })}
      </FormGrid>
    </>
  );
}
