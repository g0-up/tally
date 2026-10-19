import { toast } from "../../../shared/ui/toast-store";

/** Copies `text` and confirms with `copied`; when the browser refuses, `manual` says what to do instead. */
export function copyText(text: string, copied: string, manual: string) {
  if (!navigator.clipboard) {
    toast.error(`Trình duyệt không cho sao chép. ${manual}`);
    return;
  }
  navigator.clipboard.writeText(text).then(
    () => toast.success(copied),
    () => toast.error(`Không sao chép được. ${manual}`),
  );
}
