import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../../shared/ui/button";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useCategories } from "../../catalog";
import { useCountList } from "../api/queries";
import { CountDrawer } from "../components/count-drawer";
import { CountTable } from "../components/count-table";
import { DocNotFoundDrawer } from "../components/doc-not-found-drawer";
import { NewCountDialog } from "../containers/new-count-dialog";
import { useDocParam } from "../use-doc-param";

/** Kiểm kê (/inventory/counts). `?doc=KK-…` opens a count. */
export default function CountsPage() {
  const list = useCountList();
  const categories = useCategories();
  // A linked count may be older than the loaded pages; the server finds it by its number.
  const linkedDoc = useSearchParams()[0].get("doc");
  const linked = useCountList(linkedDoc, linkedDoc !== null);
  const known = useMemo(() => [...list.items, ...linked.items], [list.items, linked.items]);
  const shown = useDocParam(known, !list.isLoading && (linkedDoc === null || !linked.isLoading));
  const [starting, setStarting] = useState(false);
  const categoryName = (id: string | null | undefined) => categories.data?.find((c) => c.id === id)?.name ?? null;

  return (
    <>
      <PageHeader
        lead="Đếm theo số chụp tại thời điểm bắt đầu. Hoàn tất đếm, xem chênh lệch rồi mới xác nhận ghi sổ; không ghi đè âm thầm số hiện tại."
        actions={
          <Button variant="primary" onClick={() => setStarting(true)}>
            Bắt đầu phiên kiểm kê
          </Button>
        }
      />
      {list.isLoading ? <ListLoading /> : <CountTable rows={list.items} categoryName={categoryName} onOpen={(c) => shown.open(c.id)} />}
      <ListFooter list={list} />
      {shown.id !== null ? (
        <CountDrawer id={shown.id} categoryName={categoryName} onClose={shown.close} />
      ) : shown.missing !== null ? (
        <DocNotFoundDrawer title="Kiểm kê" docNo={shown.missing} onClose={shown.close} />
      ) : null}
      {starting ? <NewCountDialog onClose={() => setStarting(false)} onStarted={(c) => shown.open(c.id)} /> : null}
    </>
  );
}
