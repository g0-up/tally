// Document numbers ("SO-0042", "PN-0001/2") open the drawer of their document type. Features
// register an opener per prefix; DocNo renders a link when an opener matches and plain bold text
// otherwise, exactly like the prototype `docLink`.

export type DocOpener = (docNo: string) => void;

const openers = new Map<string, DocOpener>();

/** Registers the drawer opener for a prefix ("SO", "DS-0"); returns an unregister function. */
export function registerDocOpener(prefix: string, open: DocOpener): () => void {
  if (prefix.trim() === "") throw new Error("Document prefix must not be empty");
  openers.set(prefix, open);
  return () => {
    if (openers.get(prefix) === open) openers.delete(prefix);
  };
}

/** The document a number refers to: a line suffix ("/2") and the COD settlement suffix are dropped. */
export function normalizeDocNo(docNo: string): string {
  return (docNo.split("/")[0] ?? docNo).replace(/-COD$/, "");
}

/** Opener for a document number by its longest registered prefix, or null. */
export function resolveDocOpener(docNo: string): ((docNo: string) => void) | null {
  let best: string | null = null;
  for (const prefix of openers.keys()) {
    if (docNo.startsWith(prefix) && (best === null || prefix.length > best.length)) best = prefix;
  }
  if (best === null) return null;
  const open = openers.get(best);
  return open ? () => open(normalizeDocNo(docNo)) : null;
}

/** Document prefixes and the document they number (UI spec §7), for labels and tests. */
export const DOC_PREFIXES: Readonly<Record<string, string>> = {
  SO: "Hóa đơn",
  BG: "Báo giá",
  GH: "Giỏ hàng POS",
  BH: "Chứng từ bán",
  PX: "Phiếu xuất kho",
  HD: "Bán tại quầy",
  DH: "Đổi hàng",
  PO: "Đơn nhập",
  PN: "Phiếu nhận",
  XT: "Phiếu xuất trả NCC",
  PT: "Phiếu thu",
  PC: "Phiếu chi",
  LD: "Lệnh đóng giỏ",
  TG: "Tháo giỏ",
  KK: "Kiểm kê",
  DC: "Điều chỉnh kho",
  TH: "Phiếu trả hàng",
  DS: "Bảng kê đối soát",
  DN: "Điều chỉnh công nợ",
  BT: "Biên bản bù trừ",
};

/** Test helper: forget every registered opener. */
export function clearDocOpeners(): void {
  openers.clear();
}
