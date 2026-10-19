import { useMutation, useQueryClient } from "@tanstack/react-query";
import { errorMessage, isApiError, isSessionExpired } from "../../../shared/api/api-error";
import { useApiClient } from "../../../shared/api/api-context";
import { useServerNow } from "../../../shared/api/platform-queries";
import { platformKeys } from "../../../shared/api/query-keys";
import { useCommit } from "../../../shared/hooks/use-commit";
import { toast } from "../../../shared/ui/toast-store";
import { notificationSchema } from "../schemas";
import { notificationKeys } from "./queries";

// Write side of the notifications. `:read`, `:resolve` and `:snooze` take no Idempotency-Key
// (OpenAPI): each sets a state, so sending it twice changes nothing. Every one refreshes the list
// and the unread count of the util bar.

export type NotificationCommand = "resolve" | "snooze";

export interface NotificationCommandVars {
  id: string;
  command: NotificationCommand;
}

/**
 * How long "Nhắc lại mai" hides a notification, counted on the server's clock: the server refuses
 * an `until` out of its own range, and the device's clock may be off.
 */
const SNOOZE_MS = 24 * 60 * 60_000;

const COMMAND_TOASTS: Readonly<Record<NotificationCommand, string>> = {
  resolve: "Đã đánh dấu đã xử lý.",
  snooze: "Sẽ nhắc lại sau 1 ngày.",
};

export const READ_FAILED = "Chưa đánh dấu đã đọc.";

const ERROR_MESSAGES: Readonly<Record<string, string>> = {
  notification_resolved: "Thông báo đã được xử lý.",
  not_found: "Không tìm thấy thông báo. Tải lại để xem danh sách mới.",
};

/** The same refusal with a sentence of this screen as its message. */
function withNotificationMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return e.withMessage(message);
}

/** A refusal in this screen's words first, then the shared ones. */
const notificationErrorMessage = (e: unknown) => (isApiError(e) ? (ERROR_MESSAGES[e.code] ?? errorMessage(e)) : errorMessage(e));

/** "Đã xử lý" and "Nhắc lại mai" on one notification. */
export function useNotificationCommand() {
  const client = useApiClient();
  const serverNow = useServerNow();
  return useCommit({
    mutationFn: async (v: NotificationCommandVars) => {
      const body = v.command === "snooze" ? { until: new Date(serverNow() + SNOOZE_MS).toISOString() } : undefined;
      try {
        return await client.post(`/notifications/${v.id}:${v.command}`, body, { schema: notificationSchema });
      } catch (e) {
        throw withNotificationMessage(e);
      }
    },
    success: (_n, v) => COMMAND_TOASTS[v.command],
    invalidate: [notificationKeys.all, platformKeys.unreadCount()],
  });
}

/**
 * Marks a notification read after "Mở" or "Tải về" took the owner elsewhere. It runs in the
 * background, so a refusal says what did not happen instead of "Chưa ghi sổ.".
 */
export function useMarkNotificationRead() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (id: string) => client.post(`/notifications/${id}:read`, undefined, { schema: notificationSchema }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: notificationKeys.all });
      void queryClient.invalidateQueries({ queryKey: platformKeys.unreadCount() });
    },
    // An expired session is the app-wide handler's.
    onError: (e) => (isSessionExpired(e) ? undefined : toast.error(`${READ_FAILED} ${notificationErrorMessage(e)}`)),
  });
  return (id: string) => mutation.mutate(id);
}
