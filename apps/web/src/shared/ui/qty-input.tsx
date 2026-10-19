import { parseQtyInput, type Qty } from "../domain/qty";
import { DecimalInput, type DecimalInputProps } from "./money-input";

/** Quantity input (comma decimal mark, dot grouping); emits Qty (decimal string) or null. */
export function QtyInput(props: DecimalInputProps<Qty>) {
  return <DecimalInput<Qty> {...props} parse={parseQtyInput} />;
}
