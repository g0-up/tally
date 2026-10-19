import type { StatusTone } from "../../../shared/domain/status-tone";
import type {
  ClosureStatus,
  ExportStatus,
  IncidentImpact,
  IncidentStatus,
  Invoice,
  InvoiceStatus,
  SubscriptionStatus,
  SupportModule,
  SupportSession,
  TenantStatus,
  TicketPriority,
  TicketStatus,
} from "../api/schemas";

// Vietnamese labels of the admin API's enum values, with the pill tone each status takes.

export interface Labelled {
  label: string;
  tone: StatusTone;
}

export const TENANT_STATUS: Record<TenantStatus, Labelled> = {
  active: { label: "Hoạt động", tone: "ok" },
  read_only: { label: "Chỉ xem", tone: "warn" },
  closed: { label: "Đã đóng", tone: "mute" },
};

export const SUBSCRIPTION_STATUS: Record<SubscriptionStatus, Labelled> = {
  trialing: { label: "Dùng thử", tone: "info" },
  active: { label: "Đang dùng", tone: "ok" },
  grace: { label: "Ân hạn", tone: "warn" },
  read_only: { label: "Chỉ xem", tone: "warn" },
  closed: { label: "Đã đóng", tone: "mute" },
};

export const CLOSURE_STATUS: Record<ClosureStatus, Labelled> = {
  requested: { label: "Đã yêu cầu", tone: "info" },
  verified: { label: "Đã xác minh", tone: "info" },
  waiting: { label: "Đang chờ hết hạn", tone: "warn" },
  exporting: { label: "Đang xuất dữ liệu cuối", tone: "warn" },
  export_failed: { label: "Xuất dữ liệu lỗi", tone: "bad" },
  completed: { label: "Hoàn tất", tone: "ok" },
  cancelled: { label: "Đã hủy", tone: "mute" },
};

export const EXPORT_STATUS: Record<ExportStatus, Labelled> = {
  queued: { label: "Đang chờ", tone: "info" },
  running: { label: "Đang chạy", tone: "warn" },
  succeeded: { label: "Hoàn tất", tone: "ok" },
  failed: { label: "Thất bại", tone: "bad" },
  expired: { label: "Hết hạn", tone: "mute" },
  cancelled: { label: "Đã hủy", tone: "mute" },
};

export const INVOICE_STATUS: Record<InvoiceStatus, Labelled> = {
  open: { label: "Chưa thanh toán", tone: "warn" },
  paid: { label: "Đã thanh toán", tone: "ok" },
  void: { label: "Đã hủy", tone: "mute" },
};

export const INVOICE_PURPOSE: Record<Invoice["purpose"], string> = {
  new_period: "Kỳ mới",
  renewal: "Gia hạn",
  plan_change: "Đổi gói",
  addon: "Gói bổ sung",
};

export const INCIDENT_STATUS: Record<IncidentStatus, Labelled> = {
  investigating: { label: "Đang điều tra", tone: "bad" },
  identified: { label: "Đã xác định nguyên nhân", tone: "warn" },
  monitoring: { label: "Đang theo dõi", tone: "info" },
  resolved: { label: "Đã khắc phục", tone: "ok" },
};

export const INCIDENT_IMPACT: Record<IncidentImpact, string> = {
  none: "Không ảnh hưởng",
  minor: "Nhẹ",
  major: "Nghiêm trọng",
  critical: "Rất nghiêm trọng",
};

export const TICKET_STATUS: Record<TicketStatus, Labelled> = {
  open: { label: "Mới", tone: "info" },
  waiting_customer: { label: "Chờ cửa hàng phản hồi", tone: "warn" },
  in_progress: { label: "Đang xử lý", tone: "warn" },
  resolved: { label: "Đã giải quyết", tone: "ok" },
  closed: { label: "Đã đóng", tone: "mute" },
};

export const TICKET_PRIORITY: Record<TicketPriority, string> = {
  low: "Thấp",
  normal: "Bình thường",
  high: "Cao",
  urgent: "Khẩn cấp",
};

export const SESSION_STATE: Record<SupportSession["state"], Labelled> = {
  active: { label: "Đang hiệu lực", tone: "ok" },
  expired: { label: "Hết hạn", tone: "mute" },
  revoked: { label: "Đã thu hồi", tone: "mute" },
};

export const SUPPORT_MODULE: Record<SupportModule, string> = {
  catalog: "Hàng hóa",
  partners: "Đối tác",
  inventory: "Kho",
  procurement: "Mua hàng",
  sales: "Bán hàng",
  debt_cash: "Công nợ & quỹ",
  kits: "Bộ quà",
  campaign: "Chiến dịch",
  crm: "Khách hàng",
  reports: "Báo cáo",
  settings: "Thiết lập",
};

/** Typed entries of a label map, in declaration order. */
export function entriesOf<K extends string, V>(map: Record<K, V>): [K, V][] {
  return Object.entries(map) as [K, V][];
}
