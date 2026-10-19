import { useCursorList } from "../../../shared/hooks/use-cursor-list";
import { notificationSchema } from "../schemas";

// Read side of "Thông báo & việc cần làm". Keys start with "notifications"; the util bar's unread
// count lives under the platform keys and is refreshed beside them.

export const notificationKeys = {
  all: ["notifications"] as const,
  active: ["notifications", "active"] as const,
};

/** Unresolved notifications that are not snoozed, newest first (`GET /notifications?state=active`). */
export function useActiveNotifications() {
  return useCursorList({ queryKey: notificationKeys.active, path: "/notifications", item: notificationSchema, params: { state: "active" }, limit: 200 });
}
