import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useNavigate } from "react-router";
import { ADMIN_LOGIN_PATH } from "../../../app/guards/login-redirect";
import { isSessionExpired } from "../../../shared/api/api-error";
import { useApiClient } from "../../../shared/api/api-context";
import { adminKeys } from "../../../shared/api/query-keys";
import { signedOutSchema } from "./schemas";

// Signing out of the console. The admin session itself is read by the shared GET /me query
// (`useAdminSession`), which the guard and the header use.

/**
 * POST /auth/logout on the admin API, then /admin/login. The console is left before its cached
 * data is dropped, so nothing re-asks the API on the way out; a session that had already ended
 * (401) ends the same way.
 */
export function useAdminLogout() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const leave = useCallback(
    async (csrfToken: string | undefined) => {
      await navigate(ADMIN_LOGIN_PATH, { replace: true });
      client.setCsrfToken(csrfToken ?? null);
      queryClient.removeQueries({ queryKey: adminKeys.all });
    },
    [client, navigate, queryClient],
  );
  return useMutation({
    mutationFn: () => client.post("/auth/logout", undefined, { silent: true, schema: signedOutSchema }),
    onSuccess: (res) => leave(res.csrf_token),
    onError: async (e) => {
      if (isSessionExpired(e)) await leave(undefined);
    },
  });
}
