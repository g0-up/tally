// The app menu: single source of the 40 menu routes, the sidebar, the module tabs, page headers
// and the visual-test mapping to prototype view ids ("mod.sub"). Order and labels follow the
// prototype MENU verbatim; paths are the product's English URLs.

/** Feature folders that own menu pages. */
export type FeatureName =
  | "dashboard"
  | "notifications"
  | "pos"
  | "sales"
  | "procurement"
  | "inventory"
  | "kits"
  | "debt"
  | "campaign"
  | "crm"
  | "reports"
  | "catalog"
  | "partners"
  | "pricing"
  | "settings";

/** Subscription modules sold separately (plans.modules); other routes are in every plan. */
export type EntitlementModule = "pos" | "kits" | "campaign" | "crm";

export interface MenuSub {
  /** Prototype sub id ("ton"). */
  id: string;
  /** Sidebar and tab label. */
  label: string;
  /** Last path segment ("stock"). */
  segment: string;
  /** Page h1; the sub label unless the prototype view uses another heading. */
  title: string;
  feature: FeatureName;
  module?: EntitlementModule;
}

export interface MenuGroup {
  /** Prototype module id ("kho"). */
  id: string;
  label: string;
  /** First path segment ("inventory"). */
  segment: string;
  subs: readonly MenuSub[];
}

function sub(id: string, label: string, segment: string, feature: FeatureName, extra: { title?: string; module?: EntitlementModule } = {}): MenuSub {
  return { id, label, segment, title: extra.title ?? label, feature, ...(extra.module ? { module: extra.module } : {}) };
}

export const MENU: readonly MenuGroup[] = [
  {
    id: "tong-quan",
    label: "Tổng quan",
    segment: "overview",
    subs: [
      sub("dashboard", "Bảng điều khiển", "dashboard", "dashboard", { title: "Thu, chi và công nợ" }),
      sub("thong-bao", "Thông báo & việc cần làm", "notifications", "notifications"),
    ],
  },
  {
    id: "ban-hang",
    label: "Bán hàng",
    segment: "sales",
    subs: [
      sub("pos", "POS & giỏ hàng", "pos", "pos", { module: "pos" }),
      sub("bao-gia", "Báo giá", "quotes", "sales"),
      sub("don-ban", "Hóa đơn", "invoices", "sales"),
      sub("tra-hang", "Trả hàng", "returns", "sales"),
    ],
  },
  {
    id: "mua-hang",
    label: "Nhập hàng",
    segment: "purchasing",
    subs: [
      sub("po", "Đơn nhập", "orders", "procurement"),
      sub("hang-hoa", "Danh sách hàng hóa", "products", "procurement"),
      sub("nhan-hang", "Nhận hàng", "receipts", "procurement"),
      sub("tra-ncc", "Trả NCC", "supplier-returns", "procurement", { title: "Trả nhà cung cấp" }),
    ],
  },
  {
    id: "kho",
    label: "Kho",
    segment: "inventory",
    subs: [
      sub("ton", "Tồn kho", "stock", "inventory"),
      sub("giu-hang", "Giữ hàng", "reservations", "inventory"),
      sub("kiem-ke", "Kiểm kê", "counts", "inventory"),
      sub("lo-han", "Lô & hạn dùng", "lots", "inventory"),
      sub("dieu-chinh", "Điều chỉnh", "adjustments", "inventory", { title: "Điều chỉnh kho" }),
      sub("the-kho", "Thẻ kho", "stock-card", "inventory"),
    ],
  },
  {
    id: "gio-qua",
    label: "Giỏ quà",
    segment: "gift-baskets",
    subs: [
      sub("cong-thuc", "Giỏ quà & combo", "recipes", "kits", { module: "kits" }),
      sub("lenh-dong", "Lệnh đóng giỏ", "assembly-orders", "kits", { module: "kits" }),
      sub("tien-do", "Tiến độ & truy xuất lô", "progress", "kits", { module: "kits" }),
      sub("thao-gio", "Tháo giỏ", "disassembly", "kits", { module: "kits" }),
    ],
  },
  {
    id: "cong-no",
    label: "Công nợ",
    segment: "debts",
    subs: [
      sub("khach", "Khách còn nợ", "receivables", "debt"),
      sub("ncc", "Cửa hàng còn nợ NCC", "payables", "debt"),
      sub("ung-truoc", "Ứng trước & cọc", "advances", "debt"),
      sub("doi-soat", "Đối soát", "statements", "debt"),
      sub("lich", "Lịch thu/trả", "schedule", "debt"),
    ],
  },
  {
    id: "chien-dich",
    label: "Chiến dịch & khách",
    segment: "campaigns",
    subs: [
      sub("mua", "Chiến dịch mùa vụ", "seasons", "campaign", { module: "campaign" }),
      sub("khach-hang", "Khách hàng (CRM)", "customers", "crm", { module: "crm" }),
    ],
  },
  {
    id: "bao-cao",
    label: "Báo cáo",
    segment: "reports",
    subs: [
      sub("nxt", "Nhập–xuất–tồn", "stock-movement", "reports"),
      sub("doanh-thu", "Bán hàng", "sales", "reports"),
      sub("lai-gop", "Lãi gộp", "gross-profit", "reports", { title: "Lãi gộp & kết quả quản trị" }),
      sub("tuoi-no", "Tuổi nợ", "debt-aging", "reports"),
      sub("dong-tien", "Dòng tiền", "cash-flow", "reports"),
      sub("gio", "Giỏ quà", "gift-baskets", "reports"),
    ],
  },
  {
    id: "danh-muc",
    label: "Danh mục",
    segment: "catalog",
    subs: [
      sub("hang-hoa", "Hàng hóa", "products", "catalog"),
      sub("nha-cung-cap", "Nhà cung cấp", "suppliers", "partners"),
      sub("bang-gia", "Bảng giá", "price-lists", "pricing"),
      sub("nhom-mix", "Nhóm mix vị", "mix-groups", "catalog"),
    ],
  },
  {
    id: "thiet-lap",
    label: "Thiết lập",
    segment: "settings",
    subs: [
      sub("tai-khoan", "Tài khoản & bảo mật", "account", "settings"),
      sub("thue-bao", "Thuê bao", "subscription", "settings"),
      sub("nhat-ky", "Nhật ký", "audit-log", "settings"),
    ],
  },
];

export const HOME_PATH = "/overview/dashboard";
export const NOTIFICATIONS_PATH = "/overview/notifications";
export const SUBSCRIPTION_PATH = "/settings/subscription";

export function groupPath(group: MenuGroup): string {
  return `/${group.segment}`;
}

export function subPath(group: MenuGroup, s: MenuSub): string {
  return `/${group.segment}/${s.segment}`;
}

export interface MenuRoute {
  group: MenuGroup;
  sub: MenuSub;
  path: string;
  /** Prototype view id "mod.sub". */
  viewId: string;
}

/** Every menu route in menu order. */
export const MENU_ROUTES: readonly MenuRoute[] = MENU.flatMap((group) =>
  group.subs.map((s) => ({ group, sub: s, path: subPath(group, s), viewId: `${group.id}.${s.id}` })),
);

/** First sub of a group: target of the group path and of unknown subs. */
export function firstSubPath(group: MenuGroup): string {
  const first = group.subs[0];
  if (!first) throw new Error(`Menu group "${group.id}" has no sub`);
  return subPath(group, first);
}

/** Path of a prototype view id ("kho.ton" → "/inventory/stock"); unknown sub → first sub of its module. */
export function pathForViewId(viewId: string): string | null {
  const [mod, s] = viewId.split(".");
  const group = MENU.find((g) => g.id === mod);
  if (!group) return null;
  const hit = group.subs.find((x) => x.id === s);
  return hit ? subPath(group, hit) : firstSubPath(group);
}

/** The group a path belongs to, by its first segment. */
export function groupOfPath(pathname: string): MenuGroup | undefined {
  const seg = pathname.split("/")[1];
  return MENU.find((g) => g.segment === seg);
}
