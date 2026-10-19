import { useMemo } from "react";
import { useNavigate } from "react-router";
import { dec } from "../../../shared/lib/decimal";
import { Grid } from "../../../shared/ui/grid";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useColorDirectory } from "../../catalog";
import { useLoadAll } from "../../inventory";
import { useAssemblies, useAssemblyList, useKitLots, useKitProgress } from "../api/queries";
import { DemandCard } from "../components/demand-card";
import { FinishedLotsCard } from "../components/finished-lots-card";
import { GenealogyTable, type GenealogyRow } from "../components/genealogy-table";

/** Tiến độ & truy xuất lô (/gift-baskets/progress). */
export default function ProgressPage() {
  const progress = useKitProgress();
  const lots = useKitLots();
  // Kit lots holding stock are few; all of them are shown.
  useLoadAll(lots);
  const orders = useAssemblyList();
  const colors = useColorDirectory();
  const navigate = useNavigate();
  const posted = useMemo(() => orders.items.filter((a) => dec(a.completed_qty).gt(0)).map((a) => a.id), [orders.items]);
  const details = useAssemblies(posted);
  const rows = useMemo<GenealogyRow[]>(
    () =>
      posted.flatMap((id) => {
        const order = details.get(id);
        return order ? order.postings.map((posting) => ({ order, posting })) : [];
      }),
    [posted, details],
  );

  return (
    <>
      <PageHeader lead="Theo dõi giỏ cần cho đơn, đã đóng, đã giao; truy từ lô giỏ về lô thành phần (genealogy)." />
      <Grid cols="g2">
        {progress.isLoading ? <ListLoading /> : <DemandCard rows={progress.data ?? []} />}
        {lots.isLoading ? <ListLoading /> : <FinishedLotsCard lots={lots.items} />}
      </Grid>
      <h3 className="sec-h" style={{ margin: "22px 0 10px" }}>
        Truy xuất lô thành phần
      </h3>
      {orders.isLoading ? (
        <ListLoading />
      ) : (
        <GenealogyTable rows={rows} colors={colors} onOpen={(o) => void navigate(`/gift-baskets/assembly-orders?doc=${encodeURIComponent(o.doc_no)}`)} />
      )}
      {/* Older orders page in, each with its postings. */}
      <ListFooter list={orders} />
    </>
  );
}
