import { useState } from "react";
import { Button } from "../../../shared/ui/button";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { QUOTE_STATUS_LABELS } from "../api/adapters";
import { useCustomerDirectory, useQuoteList } from "../api/queries";
import { QuoteTable } from "../components/quote-table";
import { SalesOverlayHost } from "../containers/sales-overlay-host";
import { useQuoteActions } from "../hooks/use-quote-actions";
import { useSalesOverlay } from "../hooks/use-sales-overlay";
import type { QuoteStatus } from "../schemas";

type QuoteFilter = "all" | QuoteStatus;

const FILTERS: readonly QuoteStatus[] = ["draft", "sent", "accepted", "rejected", "expired", "converted"];

function QuotesView() {
  const overlay = useSalesOverlay();
  const list = useQuoteList();
  const customers = useCustomerDirectory();
  const actions = useQuoteActions();
  const [filter, setFilter] = useState<QuoteFilter>("all");

  const quotes = list.items;
  const shown = filter === "all" ? quotes : quotes.filter((q) => q.status === filter);
  const more = list.hasMore ? "+" : "";
  const count = (s: QuoteStatus) => quotes.filter((q) => q.status === s).length;

  return (
    <>
      <PageHeader
        lead="Vòng đời: Nháp → Đã gửi → Khách chấp nhận hoặc từ chối → Chuyển thành hóa đơn. Quá ngày hiệu lực tự chuyển “Hết hạn”. Báo giá không giữ tồn và không tạo doanh thu."
        actions={
          <Button variant="primary" onClick={() => overlay.openDialog({ kind: "quoteForm", mode: "new" })}>
            Tạo báo giá
          </Button>
        }
      />
      <div className="filters">
        <ToggleGroup<QuoteFilter>
          label="Lọc theo trạng thái"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "all", label: `Tất cả (${quotes.length}${more})` },
            ...FILTERS.map((s) => ({ value: s, label: `${QUOTE_STATUS_LABELS[s]} (${count(s)}${more})` })),
          ]}
        />
      </div>
      {list.isLoading ? <ListLoading /> : <QuoteTable quotes={shown} customerName={customers.name} pending={actions.pending} onAction={actions.run} />}
      <ListFooter list={list} />
    </>
  );
}

/** Báo giá (/sales/quotes). */
export default function QuotesPage() {
  return (
    <SalesOverlayHost>
      <QuotesView />
    </SalesOverlayHost>
  );
}
