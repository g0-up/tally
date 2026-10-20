import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage } from "../../../shared/api/api-error";
import type { ScanOutcome, ScanSource } from "../../../shared/lib/barcode";
import { matchesAllTokens } from "../../../shared/lib/textnorm";
import { Callout } from "../../../shared/ui/callout";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { toast } from "../../../shared/ui/toast-store";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { lookupSkuCode } from "../../catalog";
import { useColorMaps, useCustomerDirectory } from "../../sales/api/queries";
import { PhonePairingCard } from "../../scanner";
import { linesToDraft, type ItemDraft } from "../../sales/components/line-items/draft";
import { SalesOverlayHost } from "../../sales/containers/sales-overlay-host";
import type { PosCart, PosTile } from "../../sales/schemas";
import { tileAvailable, tileColors } from "../api/adapters";
import { useCopyCart } from "../api/mutations";
import { posCartQuery, useCartDetails, usePosBootstrap, usePosCarts, useTilePrices } from "../api/queries";
import { ScanInput } from "../components/scan-input";
import { SavedCartsTable } from "../components/saved-carts-table";
import { MAX_TILES, TileGrid } from "../components/tile-grid";
import { DeleteCartDialog } from "../containers/delete-cart-dialog";
import { PosCartContainer } from "../containers/pos-cart-container";
import { EMPTY_CART, usePosCart } from "../hooks/use-pos-cart";
import { posScan, tileProduct } from "../lib/scan-match";
import { BlockedSalesCallout } from "../offline/components/blocked-sales-callout";
import { ConnectionStatus } from "../offline/components/connection-status";
import { OfflineSimulationToggle } from "../offline/components/offline-simulation-toggle";
import { SyncExceptionDrawer } from "../offline/components/sync-exception-drawer";
import { SyncExceptionsCallout } from "../offline/components/sync-exceptions-callout";
import { useOfflinePos } from "../offline/use-offline-pos";

type CartFilter = "open" | "sold" | "all";

const OFFLINE_LOCKED = "Cần kết nối mạng.";

/** The exception drawer is part of the URL (/sales/pos?exceptions=open), so it can be linked to. */
function useExceptionsParam() {
  const [params, setParams] = useSearchParams();
  const set = (open: boolean) =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        if (open) next.set("exceptions", "open");
        else next.delete("exceptions");
        return next;
      },
      { replace: !open },
    );
  return { open: params.get("exceptions") === "open", show: () => set(true), hide: () => set(false) };
}

function PosView() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const bootstrap = usePosBootstrap();
  const colors = useColorMaps();
  const directory = useCustomerDirectory();
  const cart = usePosCart();
  const scanRef = useRef<HTMLInputElement>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<CartFilter>("open");
  const [deleting, setDeleting] = useState<PosCart | null>(null);
  const offline = useOfflinePos();
  const exceptions = useExceptionsParam();
  // Without the API the counter sells from the catalog saved on this device, at its retail prices.
  const fromDevice = offline.offline && offline.bundle !== null;

  const tiles = useMemo(() => (fromDevice ? (offline.bundle?.tiles ?? []) : (bootstrap.data?.tiles ?? [])), [fromDevice, offline.bundle, bootstrap.data]);
  const matches = useMemo(() => tiles.filter((t) => matchesAllTokens(q, t.sku_code, t.name, t.variant_label)), [tiles, q]);
  const partyId = cart.state.customer?.id ?? null;
  const shown = useMemo(() => matches.slice(0, MAX_TILES), [matches]);
  const prices = useTilePrices(shown, partyId);
  const priceOf = (t: PosTile) => {
    if (partyId === null || offline.offline) return t.retail_price;
    const data = prices.data;
    return data && data.partyId === partyId ? (data.prices.get(t.sku_id)?.amount ?? null) : null;
  };

  const add = (t: PosTile) => cart.add(tileProduct(t, t.unit_id, colors.codeOf));
  /** Adds the line a scanned code names; the scan box and a paired phone both come here. */
  const scanCode = (code: string, source: ScanSource): Promise<ScanOutcome> =>
    posScan(code, source, {
      tiles,
      online: !offline.offline,
      codeOf: colors.codeOf,
      lookup: (c) => lookupSkuCode(client, c),
      add: cart.add,
      notify: (message) => toast.error(message),
    });
  // A paired phone's code runs the same logic as Enter but leaves the text typed in the box alone.
  const phoneScan = (code: string) => scanCode(code, "phone");
  // Enter takes the text out of the box at once, so a scanner gun's next code starts in an empty box
  // and a second Enter has nothing to send. Codes are handled one after another, in the order they
  // were entered. A code that added nothing goes back into the box, unless something was typed since.
  const box = useRef("");
  const scans = useRef<Promise<void>>(Promise.resolve());
  const changeBox = (text: string) => {
    box.current = text;
    setQ(text);
  };
  const submitScan = () => {
    const code = box.current.trim();
    if (!code) return;
    changeBox("");
    const run = async () => {
      let added = false;
      try {
        added = (await scanCode(code, "keyboard")).outcome === "added";
      } catch (e) {
        toast.error(`Không thêm được “${code}”: ${errorMessage(e)}`);
      }
      if (!added && box.current === "") changeBox(code);
    };
    scans.current = scans.current.then(run);
  };

  const open = usePosCarts("open");
  const sold = usePosCarts("sold");
  const carts = filter === "open" ? open.items : filter === "sold" ? sold.items : [...open.items, ...sold.items];
  const list = filter === "sold" ? sold : open;
  const details = useCartDetails(carts.map((c) => c.id));
  const linesOf = (id: string) => details.find((d) => d.data?.id === id)?.data?.lines;
  const copy = useCopyCart();

  const edit = async (c: PosCart) => {
    if (c.status === "sold") {
      toast.error("Giỏ đã bán thành công, không sửa được.");
      return;
    }
    try {
      const detail = await queryClient.fetchQuery(posCartQuery(client, c.id));
      const lines = linesToDraft(detail.lines ?? [], colors, []).filter((l): l is ItemDraft => l.kind === "item");
      const customer = detail.customer_party_id === null ? null : { id: detail.customer_party_id, name: directory.name(detail.customer_party_id) };
      cart.load({ ...EMPTY_CART, lines, customer, cart: { id: detail.id, docNo: detail.doc_no, version: detail.version } });
      window.scrollTo(0, 0);
      toast.success(
        <>
          Đang sửa giỏ <b>{detail.doc_no}</b> của {directory.name(detail.customer_party_id)}.
        </>,
      );
    } catch (e) {
      toast.error(`Không mở được giỏ ${c.doc_no}: ${errorMessage(e)}`);
    }
  };

  const catalog = (
    <>
      <ScanInput ref={scanRef} value={q} onChange={changeBox} onSubmit={submitScan} />
      <PhonePairingCard />
      {fromDevice ? (
        <TileGrid tiles={shown} priceOf={priceOf} availableOf={tileAvailable} colorsOf={(t) => tileColors(t, colors.codeOf)} onAdd={add} />
      ) : bootstrap.isPending ? (
        <ListLoading />
      ) : bootstrap.error ? (
        <Callout tone="bad" role="alert">
          Không tải được hàng bán: {errorMessage(bootstrap.error)}
        </Callout>
      ) : (
        <TileGrid tiles={shown} priceOf={priceOf} availableOf={tileAvailable} colorsOf={(t) => tileColors(t, colors.codeOf)} onAdd={add} />
      )}
    </>
  );

  return (
    <>
      <PageHeader
        lead={
          <>
            Quét mã vạch hoặc gõ mã rồi nhấn Enter. Phím tắt: <kbd>F2</kbd> tìm hàng · <kbd>F4</kbd> lưu giỏ · <kbd>F9</kbd> thanh toán. Giỏ đã bán thành công bị khóa,
            chỉ sao chép được.
          </>
        }
        actions={import.meta.env.DEV || offline.sandbox ? <OfflineSimulationToggle simulated={offline.simulated} /> : undefined}
      />
      <ConnectionStatus
        offline={offline.offline}
        bundle={offline.bundle}
        pending={offline.pending}
        syncing={offline.syncing}
        onSync={offline.sync}
        foreign={offline.foreign}
      />
      <SyncExceptionsCallout parked={offline.parked} onOpen={exceptions.show} />
      <BlockedSalesCallout blocked={offline.blocked} onRequeue={offline.requeue} onDrop={offline.dropBlocked} />
      <PosCartContainer
        cart={cart}
        catalog={catalog}
        methods={fromDevice ? (offline.bundle?.payment_methods ?? []) : (bootstrap.data?.payment_methods ?? [])}
        onFocusScan={() => scanRef.current?.focus()}
        onPhoneScan={phoneScan}
        offline={offline}
      />
      <div className="card-h" style={{ margin: "28px 0 12px" }}>
        <h2 className="sec-h">Giỏ hàng đã lưu</h2>
        <ToggleGroup<CartFilter>
          label="Lọc giỏ hàng"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "open", label: `Đang soạn (${open.items.length}${open.hasMore ? "+" : ""})` },
            { value: "sold", label: `Đã bán (${sold.items.length}${sold.hasMore ? "+" : ""})` },
            { value: "all", label: "Tất cả" },
          ]}
        />
      </div>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <SavedCartsTable
          carts={carts}
          linesOf={linesOf}
          customerName={directory.name}
          colors={colors}
          copying={copy.pending}
          lockedReason={offline.offline ? OFFLINE_LOCKED : null}
          onEdit={(c) => void edit(c)}
          onCopy={(c) => void copy.run(c)}
          onDelete={setDeleting}
        />
      )}
      <ListFooter list={list} />
      {filter === "all" ? <ListFooter list={sold} /> : null}
      {deleting !== null ? (
        <DeleteCartDialog
          cart={deleting}
          customerName={directory.name(deleting.customer_party_id)}
          lineCount={linesOf(deleting.id)?.filter((l) => l.parent_line_id === null).length ?? null}
          onClose={() => setDeleting(null)}
          onDeleted={(c) => {
            if (cart.state.cart?.id === c.id) cart.reset();
          }}
        />
      ) : null}
      <SyncExceptionDrawer open={exceptions.open} onClose={exceptions.hide} parked={offline.parked} tenantId={offline.tenantId} />
    </>
  );
}

/** POS & giỏ hàng (/sales/pos); sale numbers open the sale-document drawer (return, exchange, print). */
export default function PosPage() {
  return (
    <SalesOverlayHost>
      <PosView />
    </SalesOverlayHost>
  );
}
