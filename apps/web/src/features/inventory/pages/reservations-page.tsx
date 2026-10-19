import { useMemo, useState } from "react";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useSkusByIds } from "../../catalog";
import { useReservationList } from "../api/queries";
import { ReservationTable } from "../components/reservation-table";
import { ReleaseReservationDialog } from "../containers/release-reservation-dialog";
import type { Reservation } from "../schemas";

/** Giữ hàng (/inventory/reservations). */
export default function ReservationsPage() {
  const list = useReservationList();
  const skuIds = useMemo(() => list.items.map((r) => r.sku_id), [list.items]);
  const skus = useSkusByIds(skuIds);
  const [releasing, setReleasing] = useState<Reservation | null>(null);

  return (
    <>
      <PageHeader lead="Lượng tồn đã cam kết cho hóa đơn hoặc lệnh đóng giỏ. Giữ hàng chưa phải xuất kho; không giữ vượt tồn bán được." />
      {list.isLoading ? <ListLoading /> : <ReservationTable rows={list.items} skus={skus.data} onRelease={setReleasing} />}
      <ListFooter list={list} />
      {releasing ? (
        <ReleaseReservationDialog reservation={releasing} name={skus.data?.get(releasing.sku_id)?.display_name ?? ""} onClose={() => setReleasing(null)} />
      ) : null}
    </>
  );
}
