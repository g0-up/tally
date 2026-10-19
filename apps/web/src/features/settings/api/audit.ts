import { z } from "zod";
import type { S } from "./contract";

// GET /audit-events: the store's append-only log, newest first. The API filters by entity, actor,
// action code, business dates and free text (`q`: document number, action or entity code, or the
// name of the user who acted).

export const AUDIT_PAGE_LIMIT = 200;
/** Longest `q` GET /audit-events accepts. */
export const AUDIT_SEARCH_MAX_LENGTH = 100;

export const auditEventSchema = z.object({
  id: z.string(),
  actor_type: z.enum(["owner", "support", "system", "platform_admin"]),
  actor_id: z.string().nullable(),
  action: z.string(),
  entity_type: z.string(),
  entity_id: z.string().nullable(),
  doc_no: z.string().nullable(),
  source: z.string(),
  ip: z.string().nullable(),
  created_at: z.string(),
}) satisfies z.ZodType<Omit<S["AuditEvent"], "before" | "after" | "request_id">>;
export type AuditEvent = z.infer<typeof auditEventSchema>;

export const ACTOR_TYPE: Readonly<Record<AuditEvent["actor_type"], string>> = {
  owner: "Chủ cửa hàng",
  support: "Nhân viên hỗ trợ",
  system: "Hệ thống",
  platform_admin: "Quản trị nền tảng",
};

export const AUDIT_SOURCE: Readonly<Record<string, string>> = {
  web: "Trình duyệt",
  pos: "Máy bán hàng",
  pos_offline_sync: "Đồng bộ bán ngoại tuyến",
  cli: "Dòng lệnh vận hành",
  worker: "Tác vụ nền",
  support: "Phiên hỗ trợ",
};

/** Actions whose meaning is more than "verb + object". */
const ACTION_LABELS: Readonly<Record<string, string>> = {
  "session.create": "Đăng nhập",
  "session.revoke": "Đăng xuất thiết bị",
  "user.change_password": "Đổi mật khẩu",
  "user.reset_password": "Đặt lại mật khẩu",
  "user.enable_mfa": "Bật xác thực 2 lớp",
  "user.disable_mfa": "Tắt xác thực 2 lớp",
  "user.verify_email": "Xác thực email",
  "user.reject_login": "Đăng nhập bị từ chối",
  "user.reject_reauth": "Xác nhận mật khẩu bị từ chối",
  "user.update": "Sửa tài khoản",
  "tenant.create": "Tạo cửa hàng",
  "tenant.update": "Sửa thông tin cửa hàng",
  "tenant_settings.update": "Sửa cài đặt cửa hàng",
  "tenant.request_owner_transfer": "Yêu cầu chuyển quyền chủ cửa hàng",
  "tenant.transfer_owner": "Chuyển quyền chủ cửa hàng",
  "tenant.sandbox_reset": "Đặt lại cửa hàng mẫu",
  "tenant.replace": "Tạo lại cửa hàng mẫu",
  "tenant.legal_hold_set": "Đặt giữ dữ liệu theo yêu cầu pháp lý",
  "period.lock": "Khóa sổ kỳ",
  "period.reopen": "Mở lại kỳ đã khóa",
  "exception.confirm": "Xác nhận ngoại lệ",
  "export_job.requested": "Xuất file",
  "export_job.cancelled": "Hủy xuất file",
  "onboarding.opening_confirmed": "Xác nhận số dư đầu kỳ",
  "account_closure.requested": "Yêu cầu đóng tài khoản",
  "account_closure.verified": "Xác nhận đóng tài khoản",
  "account_closure.cancelled": "Hủy đóng tài khoản",
  "account_closure.completed": "Đóng tài khoản",
  "account_closure.export_failed": "Bản xuất cuối bị lỗi",
  "account_closure.export_requeued": "Lấy lại bản xuất cuối",
  "account_closure.export_retried": "Thử lại bản xuất cuối",
  "account_closure.export_link_requested": "Gửi lại liên kết bản xuất cuối",
  "subscription.transition": "Đổi trạng thái thuê bao",
  "saas_invoice.issued": "Phát hành hóa đơn thuê bao",
  "saas_invoice.paid": "Thanh toán hóa đơn thuê bao",
  "saas_invoice.payment_refused": "Thanh toán hóa đơn thuê bao bị từ chối",
  "saas_invoice.voided": "Hủy hóa đơn thuê bao",
  "support_ticket.create": "Tạo yêu cầu hỗ trợ",
  "support_ticket.message": "Gửi tin trong yêu cầu hỗ trợ",
  "support_ticket.reply": "Hỗ trợ trả lời",
  "support_ticket.update": "Cập nhật yêu cầu hỗ trợ",
  "support_session.grant": "Cấp quyền hỗ trợ",
  "support_session.revoke": "Thu hồi quyền hỗ trợ",
  "support_session.access": "Hỗ trợ xem dữ liệu",
};

const VERBS: Readonly<Record<string, string>> = {
  create: "Tạo",
  created: "Tạo",
  record: "Ghi",
  recorded: "Ghi",
  update: "Sửa",
  updated: "Sửa",
  delete: "Xóa",
  deleted: "Xóa",
  approve: "Duyệt",
  confirm: "Xác nhận",
  confirmed: "Xác nhận",
  post: "Ghi sổ",
  posted: "Ghi sổ",
  reverse: "Đảo",
  reversed: "Đảo",
  void: "Hủy",
  cancel: "Hủy",
  cancelled: "Hủy",
  close: "Đóng",
  close_short: "Đóng thiếu",
  reopen: "Mở lại",
  start: "Bắt đầu",
  finish: "Kết thúc",
  recount: "Đếm lại",
  remind: "Nhắc",
  accept: "Chấp nhận",
  convert: "Chuyển",
};

const ENTITIES: Readonly<Record<string, string>> = {
  sale: "hóa đơn bán",
  sales_document: "hóa đơn bán",
  sales_return: "phiếu trả hàng bán",
  order: "đơn hàng",
  quote: "báo giá",
  receipt: "phiếu nhập",
  purchase_order: "đơn mua",
  purchase_return: "phiếu trả hàng mua",
  purchase_price_adjustment: "điều chỉnh giá mua",
  supplier_document: "chứng từ nhà cung cấp",
  landed_cost: "chi phí mua hàng",
  stock_out: "phiếu xuất kho",
  stock_adjustment: "điều chỉnh kho",
  stock_count: "phiếu kiểm kê",
  reservation: "giữ hàng",
  payment: "phiếu thu chi",
  refund: "phiếu hoàn tiền",
  allocation: "phân bổ thanh toán",
  obligation: "công nợ",
  debt_offset: "bù trừ công nợ",
  debt_adjustment: "điều chỉnh công nợ",
  credit_note: "giấy báo có",
  collection_note: "ghi chú thu nợ",
  statement: "bảng đối chiếu",
  fund_account: "quỹ",
  product: "hàng hóa",
  sku: "mã hàng",
  unit: "đơn vị tính",
  mix_group: "nhóm trộn",
  price: "giá",
  price_list: "bảng giá",
  promotion: "khuyến mãi",
  tax_rate: "thuế suất",
  customer: "khách hàng",
  supplier: "nhà cung cấp",
  partner: "đối tác",
  party_address: "địa chỉ đối tác",
  reason: "lý do",
  shift: "ca bán hàng",
  pos_cart: "giỏ hàng POS",
  assembly_posting: "phiếu đóng giỏ",
  disassembly: "phiếu tháo giỏ",
  campaign: "chiến dịch",
  care_task: "việc chăm sóc",
  consent: "đồng ý liên hệ",
  loyalty: "điểm thưởng",
  loyalty_program: "chương trình điểm thưởng",
  saved_report: "báo cáo đã lưu",
  reorder: "đề xuất đặt hàng",
  plan: "gói thuê bao",
  tenant: "cửa hàng",
  tenant_settings: "cài đặt cửa hàng",
  user: "tài khoản",
  session: "phiên đăng nhập",
  period_lock: "kỳ khóa sổ",
  exception_confirmation: "xác nhận ngoại lệ",
  export_job: "bản xuất",
  account_closure: "yêu cầu đóng tài khoản",
  account_closure_request: "yêu cầu đóng tài khoản",
  subscription: "thuê bao",
  saas_invoice: "hóa đơn thuê bao",
  support_ticket: "yêu cầu hỗ trợ",
  support_session: "phiên hỗ trợ",
};

/** Vietnamese name of an audit action, or its raw code when the screen does not know it. */
export function auditActionLabel(action: string): string {
  const known = ACTION_LABELS[action];
  if (known) return known;
  const dot = action.indexOf(".");
  if (dot < 0) return action;
  const verb = VERBS[action.slice(dot + 1)];
  const entity = ENTITIES[action.slice(0, dot)];
  return verb && entity ? `${verb} ${entity}` : action;
}

/** Name of the object an event is about, for rows without a document number. */
export function auditEntityLabel(entityType: string): string {
  return ENTITIES[entityType] ?? entityType;
}
