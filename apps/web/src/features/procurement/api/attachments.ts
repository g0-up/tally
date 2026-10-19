import { ApiError, networkError } from "../../../shared/api/api-error";
import { useApiClient } from "../../../shared/api/api-context";
import { useCommit } from "../../../shared/hooks/use-commit";
import { attachmentSchema, attachmentUrlSchema, uploadSchema } from "../schemas";
import { procurementKeys } from "./queries";

// Document photos of a receipt: the API signs an upload for a declared
// file, the browser PUTs the bytes straight to object storage, then `:complete` checks them. The
// receipt detail lists the clean attachments; a download goes through a short-lived signed URL.

/** Types and size the API accepts for a document photo. */
export const ATTACHMENT_TYPES = ["image/jpeg", "image/png", "image/webp", "application/pdf"] as const;
export const ATTACHMENT_MAX_BYTES = 10 * 1024 * 1024;

export const ATTACHMENT_MESSAGES = {
  type: "Chỉ nhận ảnh JPG, PNG, WEBP hoặc tệp PDF.",
  size: "Tệp lớn hơn 10 MB.",
  upload: "Không tải được tệp lên kho lưu trữ. Thử lại.",
} as const;

/** The client-side refusal of a file, in the dialog's words; null when the API would take it. */
export function attachmentProblem(file: { type: string; size: number }): string | null {
  if (!(ATTACHMENT_TYPES as readonly string[]).includes(file.type)) return ATTACHMENT_MESSAGES.type;
  if (file.size <= 0 || file.size > ATTACHMENT_MAX_BYTES) return ATTACHMENT_MESSAGES.size;
  return null;
}

async function sha256Hex(file: Blob): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** "Đính kèm ảnh chứng từ" on a receipt: sign, upload, complete. */
export function useAttachReceiptFile(receiptId: string) {
  const client = useApiClient();
  return useCommit({
    mutationFn: async (file: File) => {
      const problem = attachmentProblem(file);
      if (problem !== null) throw new ApiError({ status: 422, code: "validation_failed", message: problem });
      const signed = await client.post(
        "/attachments",
        { owner_type: "receipt", owner_id: receiptId, file_name: file.name, content_type: file.type, size_bytes: file.size, sha256: await sha256Hex(file) },
        { schema: uploadSchema },
      );
      let put: Response;
      try {
        put = await fetch(signed.upload.url, { method: signed.upload.method, headers: signed.upload.headers ?? {}, body: file });
      } catch {
        throw networkError();
      }
      if (!put.ok) throw new ApiError({ status: put.status, code: "upload_failed", message: ATTACHMENT_MESSAGES.upload });
      return client.post(`/attachments/${signed.attachment.id}:complete`, undefined, { schema: attachmentSchema });
    },
    success: (a) => `Đã đính kèm ${a.file_name}.`,
    invalidate: [procurementKeys.receipt(receiptId)],
  });
}

/** Downloads an attachment through its signed URL (the API signs it as a download). */
export function useOpenAttachment() {
  const client = useApiClient();
  return useCommit({
    mutationFn: async (attachmentId: string) => {
      const { url } = await client.get(`/attachments/${attachmentId}/url`, { schema: attachmentUrlSchema });
      window.location.assign(url);
      return url;
    },
  });
}
