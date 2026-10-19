import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { dec } from "../../../shared/lib/decimal";
import { Button } from "../../../shared/ui/button";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { DocNotFoundDrawer, useDocParam } from "../../inventory";
import { canPostAssembly } from "../api/adapters";
import { useReserveAssembly } from "../api/mutations";
import { useAssemblies, useAssemblyList, useOpenInvoices } from "../api/queries";
import { AssemblyTable } from "../components/assembly-table";
import { AssemblyCompleteDialog } from "../containers/assembly-complete-dialog";
import { AssemblyDialog } from "../containers/assembly-dialog";
import { AssemblyDrawer } from "../containers/assembly-drawer";
import type { AssemblyOrder } from "../schemas";

/** Lệnh đóng giỏ (/gift-baskets/assembly-orders). `?doc=LD-…` opens that order. */
export default function AssemblyOrdersPage() {
  const list = useAssemblyList();
  // A linked order may be older than the loaded pages; the server finds it by its number.
  const linkedDoc = useSearchParams()[0].get("doc");
  const linked = useAssemblyList(linkedDoc ?? "", linkedDoc !== null);
  const known = useMemo(() => [...list.items, ...linked.items], [list.items, linked.items]);
  const doc = useDocParam(known, !list.isLoading && (linkedDoc === null || !linked.isLoading));
  const invoices = useOpenInvoices(true).data;
  const reserve = useReserveAssembly();
  const [creating, setCreating] = useState(false);
  const [completing, setCompleting] = useState<AssemblyOrder | null>(null);

  // Orders holding components: the ones short of what they still need show what is missing.
  const openIds = useMemo(() => list.items.filter((a) => canPostAssembly(a.status)).map((a) => a.id), [list.items]);
  const details = useAssemblies(openIds);
  const shortages = useMemo(() => {
    const out = new Map<string, string>();
    for (const d of details.values()) {
      const short = d.items.filter((i) => dec(i.reserved_qty).lt(i.remaining_qty)).map((i) => i.sku_name);
      if (short.length > 0) out.set(d.id, short.join(", "));
    }
    return out;
  }, [details]);
  const invoiceDoc = (id: string) => invoices?.find((o) => o.id === id)?.doc_no;

  return (
    <>
      <PageHeader
        lead="Lên kế hoạch chưa tiêu hao nguyên liệu. Chốt lệnh sẽ giữ nguyên liệu; hoàn thành mới xuất thành phần và nhập giỏ thành phẩm trong cùng một nghiệp vụ cân bằng."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Tạo lệnh đóng
          </Button>
        }
      />
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <AssemblyTable
          rows={list.items}
          invoiceDoc={invoiceDoc}
          shortages={shortages}
          busy={reserve.pending}
          onOpen={(a) => doc.open(a.id)}
          onReserve={(a) => void reserve.run(a)}
          onComplete={setCompleting}
          empty="Chưa có lệnh đóng."
        />
      )}
      <ListFooter list={list} />
      {doc.id !== null ? <AssemblyDrawer orderId={doc.id} invoiceDoc={invoiceDoc} onClose={doc.close} /> : null}
      {doc.missing !== null ? <DocNotFoundDrawer title="Lệnh đóng" docNo={doc.missing} onClose={doc.close} /> : null}
      {creating ? <AssemblyDialog onClose={() => setCreating(false)} /> : null}
      {completing ? <AssemblyCompleteDialog order={completing} onClose={() => setCompleting(null)} /> : null}
    </>
  );
}
