import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { Button } from "../../../shared/ui/button";
import { apiError } from "../../../test/msw/handlers";
import { server } from "../../../test/msw/server";
import { renderWithProviders } from "../../../test/render";
import type { CurrentPrices, PriceDraft } from "../price-edit";
import { useSaveSalePrice } from "./mutations";

const sku = { id: "sku-1", retail_price: toMoney(15000), wholesale_price: toMoney(13000) };
/** The same SKU sold by the thùng of 12 at its own 170.000 (sỉ 150.000). */
const PACKED = {
  ...sku,
  units: [
    { unit_id: "hop", unit_name: "hộp", factor: "1", is_base: true, active: true, retail_price: toMoney(15000), wholesale_price: toMoney(13000) },
    { unit_id: "thung", unit_name: "thùng", factor: "12", is_base: false, active: true, retail_price: toMoney(170000), wholesale_price: toMoney(150000) },
  ],
};

function PriceForm({ draft, after, current = sku }: { draft: () => PriceDraft; after: () => void; current?: CurrentPrices & { id: string } }) {
  const { save, pending } = useSaveSalePrice({ sku: current, source: "catalog", after });
  return (
    <Button variant="primary" pending={pending} onClick={() => save(draft())}>
      Lưu giá
    </Button>
  );
}

type Write = { key: string | null; kind: string; price: string | null; unit_id?: string };

/** Records every price write; `answer` decides the response of each. */
function serve(answer: (w: Write, n: number) => Response | undefined) {
  const writes: Write[] = [];
  server.use(
    http.post(/\/skus\/sku-1\/sale-price$/, async ({ request }) => {
      const body = (await request.json()) as { kind: string; price: string | null; unit_id?: string };
      const w: Write = { key: request.headers.get("Idempotency-Key"), kind: body.kind, price: body.price, ...(body.unit_id === undefined ? {} : { unit_id: body.unit_id }) };
      writes.push(w);
      return answer(w, writes.filter((x) => x.kind === w.kind).length) ?? HttpResponse.json({});
    }),
  );
  return writes;
}

const draftOf = (retail: number, wholesale = 16000): PriceDraft => ({ retail: toMoney(retail), wholesale: toMoney(wholesale), allColors: false });

async function click() {
  await userEvent.setup().click(await screen.findByRole("button", { name: "Lưu giá" }));
}

describe("useSaveSalePrice", () => {
  it("writes each list under its own key and resends only the one that got no answer, under its key", async () => {
    const writes = serve((w, n) => (w.kind === "wholesale" && n === 1 ? HttpResponse.error() : undefined));
    const after = vi.fn();
    renderWithProviders(<PriceForm draft={() => draftOf(18000)} after={after} />);

    await click();
    expect(await screen.findByText(/Đã lưu một phần giá nhưng chưa rõ đã lưu giá sỉ hay chưa\./)).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();
    await click();
    await waitFor(() => expect(after).toHaveBeenCalled());

    const intent = writes[0]?.key?.split(":sale-price:")[0] ?? "";
    expect(intent).not.toBe("");
    const retailKey = `${intent}:sale-price:sku-1:retail:general:catalog:18000:1`;
    const wholesaleKey = `${intent}:sale-price:sku-1:wholesale:general:catalog:16000:1`;
    expect(writes.map((w) => w.key)).toEqual([retailKey, wholesaleKey, wholesaleKey]);
  });

  it("names the list it did not save when the other one was saved", async () => {
    const writes = serve((w) => (w.kind === "wholesale" ? apiError(422, "validation_failed") : undefined));
    const after = vi.fn();
    renderWithProviders(<PriceForm draft={() => draftOf(18000)} after={after} />);

    await click();
    expect(await screen.findByText(/^Đã lưu một phần giá nhưng chưa lưu giá sỉ: .* Bấm lại để lưu tiếp\.$/)).toBeInTheDocument();
    expect(writes.map((w) => w.kind)).toEqual(["retail", "wholesale"]);
    expect(after).not.toHaveBeenCalled();
  });

  it("writes a price changed back to one the intent already saved again, under a new key", async () => {
    // The wholesale list keeps refusing, so the three clicks are one intent.
    const writes = serve((w) => (w.kind === "wholesale" ? apiError(422, "validation_failed") : undefined));
    let retail = 18000;
    renderWithProviders(<PriceForm draft={() => draftOf(retail)} after={vi.fn()} />);

    await click();
    await waitFor(() => expect(writes).toHaveLength(2));
    retail = 20000;
    await click();
    await waitFor(() => expect(writes).toHaveLength(4));
    retail = 18000;
    await click();
    await waitFor(() => expect(writes).toHaveLength(6));

    const retailWrites = writes.filter((w) => w.kind === "retail");
    expect(retailWrites.map((w) => w.price)).toEqual(["18000", "20000", "18000"]);
    expect(new Set(retailWrites.map((w) => w.key)).size).toBe(3);
  });

  it("removes a cleared unit price with null under the unit's own key, naming the unit when it fails", async () => {
    const writes = serve((w) => (w.unit_id === "thung" && w.kind === "wholesale" ? apiError(422, "validation_failed") : undefined));
    const after = vi.fn();
    const draft = (): PriceDraft => ({ retail: toMoney(16000), wholesale: null, allColors: false, units: { thung: { retail: null, wholesale: toMoney(140000) } } });
    renderWithProviders(<PriceForm draft={draft} after={after} current={PACKED} />);

    await click();
    expect(await screen.findByText(/^Đã lưu một phần giá nhưng chưa lưu giá sỉ \/ thùng: .* Bấm lại để lưu tiếp\.$/)).toBeInTheDocument();
    expect(after).not.toHaveBeenCalled();

    const intent = writes[0]?.key?.split(":sale-price:")[0] ?? "";
    expect(writes).toEqual([
      { key: `${intent}:sale-price:sku-1:retail:general:catalog:16000:1`, kind: "retail", price: "16000" },
      { key: `${intent}:sale-price:sku-1:retail:general:unit-thung:catalog:none:1`, kind: "retail", price: null, unit_id: "thung" },
      { key: `${intent}:sale-price:sku-1:wholesale:general:unit-thung:catalog:140000:1`, kind: "wholesale", price: "140000", unit_id: "thung" },
    ]);
  });
});
