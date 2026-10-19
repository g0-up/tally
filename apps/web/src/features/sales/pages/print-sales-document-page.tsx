import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { errorMessage } from "../../../shared/api/api-error";
import { useDocumentTitle } from "../../../shared/hooks/use-document-title";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { usePrintDoc } from "../api/queries";
import { PrintDocument, type PaperSize } from "../components/print-document";
import "../print.css";

/** Tab name before the document number is known. */
const PRINT_PAGE_TITLE = "In chứng từ";

/**
 * In chứng từ bán (/print/sales-documents/:id): the server's print payload, no shell, A5 or 80 mm.
 * The page opens in its own tab; "Đóng" closes that tab, or steps back when it was reached by
 * navigating within the same tab (a browser only lets a script close a tab with no history).
 */
export default function PrintSalesDocumentPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [paper, setPaper] = useState<PaperSize>("a5");
  const doc = usePrintDoc("sales_document", id);
  useDocumentTitle(doc.data ? `${doc.data.title} ${doc.data.doc_no}` : PRINT_PAGE_TITLE);
  const close = () => {
    if (window.history.length > 1) void navigate(-1);
    else window.close();
  };

  return (
    <main className="print-page">
      <div className="print-bar">
        <ToggleGroup<PaperSize>
          label="Khổ giấy"
          value={paper}
          onChange={setPaper}
          options={[
            { value: "a5", label: "Khổ A5" },
            { value: "roll", label: "Giấy cuộn 80mm" },
          ]}
        />
        <span className="sp" />
        <Button variant="line" size="sm" onClick={close}>
          Đóng
        </Button>
        <Button variant="primary" size="sm" disabled={!doc.data} onClick={() => window.print()}>
          In phiếu
        </Button>
      </div>
      {doc.data ? (
        <PrintDocument doc={doc.data} paper={paper} />
      ) : doc.error ? (
        <Callout tone="bad" role="alert">
          Không tải được chứng từ để in: {errorMessage(doc.error)}
        </Callout>
      ) : (
        <div className="card" role="status" aria-live="polite">
          Đang tải…
        </div>
      )}
    </main>
  );
}
