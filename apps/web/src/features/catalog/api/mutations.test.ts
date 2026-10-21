import { describe, expect, it } from "vitest";
import { ApiError } from "../../../shared/api/api-error";
import { UNIT_MESSAGES } from "../components/product-draft";
import { productFormErrors, unitsInUse } from "./mutations";

const refused = (fields: Record<string, string>) => new ApiError({ status: 422, code: "validation_failed", message: "Kiểm tra lại các ô được đánh dấu.", fields });

describe("productFormErrors", () => {
  it("puts unit-row refusals under their row of the unit table", () => {
    expect(productFormErrors(refused({ "sku.units[1].unit_id": "duplicate", "sku.units[2].factor": "invalid" }))).toEqual({
      "units.1.unit": "Đơn vị bị trùng.",
      "units.2.factor": "Giá trị không hợp lệ.",
    });
  });

  it("says how many units a product may hold when the table is refused as too long", () => {
    expect(productFormErrors(refused({ "sku.units": "too_many" }))).toEqual({ units: UNIT_MESSAGES.tooMany });
    expect(productFormErrors(refused({ units: "too_many" }))).toEqual({ units: UNIT_MESSAGES.tooMany });
  });
});

describe("unitsInUse", () => {
  it("reads each unit the server would not delete with its lock, ignoring other refusals", () => {
    const inUse = new ApiError({ status: 409, code: "unit_in_use", message: "unit_in_use", fields: { "units[unit-day]": "documents", "units[unit-hop]": "barcodes", "units[unit-x]": "other" } });
    expect([...unitsInUse(inUse)]).toEqual([
      ["unit-day", "documents"],
      ["unit-hop", "barcodes"],
    ]);
    expect(unitsInUse(refused({ "units[0].factor": "invalid" })).size).toBe(0);
  });
});
