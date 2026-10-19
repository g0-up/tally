import type { StatusTone } from "../../shared/domain/status-tone";
import { dec } from "../../shared/lib/decimal";
import { vnd } from "../../shared/lib/format";
import type { ClosureStatus, ExportStatus } from "./api/account";
import type { Plan, SubscriptionStatus } from "../../shared/api/endpoints";
import type { SaasInvoice } from "./api/billing";
import type { SupportTicket, SupportViewModule, TicketPriority } from "./api/support";

// Vietnamese names of the API codes the settings screens show. A status carries its tone when the
// label alone would not give the right one.

export interface StatusLabel {
  label: string;
  tone?: StatusTone;
}

export const OWNER_STATUS: Readonly<Record<string, StatusLabel>> = {
  active: { label: "Hoạt động" },
  pending: { label: "Chờ xác thực email", tone: "warn" },
  disabled: { label: "Ngừng" },
};

export const SUBSCRIPTION_STATUS: Readonly<Record<SubscriptionStatus, StatusLabel>> = {
  trialing: { label: "Dùng thử" },
  active: { label: "Còn hạn" },
  grace: { label: "Quá hạn, đang ân hạn", tone: "warn" },
  read_only: { label: "Chỉ xem", tone: "bad" },
  closed: { label: "Đã đóng", tone: "mute" },
};

export const PLAN_KIND: Readonly<Record<Plan["kind"], string>> = {
  year_round: "Cả năm",
  seasonal: "Theo mùa",
  addon: "Gói bổ sung",
  trial: "Dùng thử",
};

export const BILLING_PERIOD: Readonly<Record<Plan["billing_period"], string>> = {
  month: "tháng",
  season: "mùa",
  year: "năm",
  none: "",
};

/** Price per billing period, or "Chưa chốt" while the plan has no price. */
export function priceText(plan: Plan): string {
  if (dec(plan.price).isZero()) return "Chưa chốt";
  const period = BILLING_PERIOD[plan.billing_period];
  return period ? `${vnd(plan.price)} / ${period}` : vnd(plan.price);
}

export const PLAN_MODULE: Readonly<Record<string, string>> = {
  pos: "Bán tại quầy",
  kits: "Giỏ quà",
  campaign: "Chiến dịch mùa vụ",
  crm: "Chăm sóc khách hàng",
  reports_advanced: "Báo cáo nâng cao",
  ai_qa: "Hỏi đáp AI",
};

/** Prototype descriptions of the plans it shows, by plan code. */
export const PLAN_DESCRIPTION: Readonly<Record<string, string>> = {
  store: "POS, nhập/xuất/tồn, công nợ, thu/chi, báo cáo cơ bản",
  wholesale: "Hóa đơn xuất kho nhiều đợt, giá theo khách, hạn mức, đối soát",
  gift_basket: "Công thức, đóng/tháo giỏ, lô, nhiều điểm nhận, tiến độ",
};

export const INVOICE_STATUS: Readonly<Record<SaasInvoice["status"], StatusLabel>> = {
  open: { label: "Chưa thanh toán" },
  paid: { label: "Đã thanh toán" },
  void: { label: "Đã hủy" },
};

export const INVOICE_PURPOSE: Readonly<Record<SaasInvoice["purpose"], string>> = {
  new_period: "Kỳ mới",
  renewal: "Gia hạn",
  plan_change: "Đổi gói",
  addon: "Gói bổ sung",
};

export const TICKET_STATUS: Readonly<Record<SupportTicket["status"], StatusLabel>> = {
  open: { label: "Đang mở" },
  waiting_customer: { label: "Chờ cửa hàng trả lời", tone: "warn" },
  in_progress: { label: "Đang xử lý" },
  resolved: { label: "Đã giải quyết", tone: "ok" },
  closed: { label: "Đã đóng", tone: "mute" },
};

export const TICKET_PRIORITY: Readonly<Record<TicketPriority, string>> = {
  low: "Thấp",
  normal: "Bình thường",
  high: "Cao",
  urgent: "Khẩn",
};

export const SUPPORT_MODULE: Readonly<Record<SupportViewModule, string>> = {
  catalog: "Danh mục",
  partners: "Đối tác",
  inventory: "Kho",
  procurement: "Mua hàng",
  sales: "Bán hàng",
  debt_cash: "Công nợ & quỹ",
  kits: "Giỏ quà",
  campaign: "Chiến dịch",
  crm: "Khách hàng",
  reports: "Báo cáo",
  settings: "Thiết lập",
};

export const EXPORT_STATUS: Readonly<Record<ExportStatus, StatusLabel>> = {
  queued: { label: "Đang chờ xử lý", tone: "info" },
  running: { label: "Đang tạo bản xuất", tone: "info" },
  succeeded: { label: "Hoàn tất" },
  failed: { label: "Lỗi", tone: "bad" },
  expired: { label: "Hết hạn tải" },
  cancelled: { label: "Đã hủy" },
};

export const CLOSURE_STATUS: Readonly<Record<ClosureStatus, StatusLabel>> = {
  requested: { label: "Chờ xác nhận qua email", tone: "warn" },
  verified: { label: "Đã xác nhận" },
  waiting: { label: "Đang trong thời gian chờ", tone: "warn" },
  exporting: { label: "Đang lấy bản xuất cuối, chỉ xem", tone: "warn" },
  export_failed: { label: "Bản xuất cuối bị lỗi, chỉ xem", tone: "bad" },
  completed: { label: "Đã đóng cửa hàng", tone: "mute" },
  cancelled: { label: "Đã hủy yêu cầu", tone: "mute" },
};

export const PERIOD_LOCK_ACTION: Readonly<Record<"lock" | "reopen", string>> = {
  lock: "Khóa sổ",
  reopen: "Mở lại kỳ",
};
