import { dec } from "../../../shared/lib/decimal";
import { fd, vnd } from "../../../shared/lib/format";
import { DetailDrawer } from "../../../shared/ui/detail-drawer";
import { DocNo } from "../../../shared/ui/doc-no";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { useCustomerDirectory, useExchange, useSalesDocument, useSalesReturn } from "../api/queries";
import { DrawerStatus } from "./drawer-status";

/** One exchange: the return and the new sale it posted, and the difference settled with the customer. */
export function ExchangeDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const query = useExchange(id);
  const customers = useCustomerDirectory();
  const x = query.data;
  const original = useSalesDocument(x?.original_sales_document_id ?? null);
  const taken = useSalesDocument(x?.new_sales_document_id ?? null);
  const returned = useSalesReturn(x?.sales_return_id ?? null);

  if (!x) {
    return (
      <DetailDrawer open onClose={onClose} title="Phiếu đổi hàng" docKey={id}>
        <DrawerStatus error={query.error} deletedText="Phiếu đổi hàng không còn." />
      </DetailDrawer>
    );
  }

  const pays = dec(x.customer_pays_amount).gt(0);
  return (
    <DetailDrawer open onClose={onClose} title={`Phiếu đổi hàng ${x.doc_no}`} sub={`${customers.name(x.customer_party_id)} · ${fd(x.business_date)}`} docKey={x.id}>
      <div className="kv">
        <div>
          <b>Chứng từ gốc</b>
          <span>
            <DocNo value={original.data?.doc_no} />
          </span>
        </div>
        <div>
          <b>Phiếu trả</b>
          <span>
            <DocNo value={returned.data?.doc_no} />
          </span>
        </div>
        <div>
          <b>Chứng từ bán mới</b>
          <span>
            <DocNo value={taken.data?.doc_no} />
          </span>
        </div>
      </div>
      <ImpactSummary
        items={[
          { label: "Giá trị hàng trả", value: vnd(x.returned_amount) },
          { label: "Giá trị hàng lấy", value: vnd(x.taken_amount) },
          pays ? { label: "Khách trả thêm", value: vnd(x.customer_pays_amount) } : { label: "Cửa hàng trả lại khách", value: vnd(x.store_refunds_amount) },
          { label: "Giảm nợ cũ của khách", value: vnd(x.debt_reduction_amount) },
        ]}
      />
    </DetailDrawer>
  );
}
