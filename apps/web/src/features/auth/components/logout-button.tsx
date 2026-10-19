import { errorMessage, isSessionExpired } from "../../../shared/api/api-error";
import { toast } from "../../../shared/ui/toast-store";
import { useLogout } from "../api/mutations";

/** "Đăng xuất" in the util bar. A failure other than an already-ended session is reported in a toast. */
export function LogoutButton({ className }: { className?: string }) {
  const logout = useLogout();
  return (
    <button
      type="button"
      className={className}
      disabled={logout.isPending}
      aria-busy={logout.isPending || undefined}
      onClick={() =>
        logout.mutate(undefined, {
          onError: (e) => {
            if (!isSessionExpired(e)) toast.error(`Không đăng xuất được: ${errorMessage(e)}`);
          },
        })
      }
    >
      {logout.isPending ? "Đang đăng xuất…" : "Đăng xuất"}
    </button>
  );
}
