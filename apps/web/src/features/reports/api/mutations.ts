import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { toast } from "../../../shared/ui/toast-store";
import { savedReportSchema, type ReportConfig, type ReportKey } from "../schemas";
import { reportKeys } from "./queries";

// Write side of the saved views ("Bộ lọc đã lưu"). They hold a layout, not a posting: OpenAPI takes
// neither an Idempotency-Key nor If-Match here, and a refusal is shown inside the dialog that sent
// it rather than as a "Chưa ghi sổ." toast.

export interface SaveReportVars {
  name: string;
  config: ReportConfig;
}

/** `POST /saved-reports`; the name is unique per report. */
export function useSaveReport(key: ReportKey) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, config }: SaveReportVars) => client.post("/saved-reports", { report_key: key, name, config }, { schema: savedReportSchema }),
    onSuccess: async () => {
      toast.success("Đã lưu bộ lọc.");
      await queryClient.invalidateQueries({ queryKey: reportKeys.saved(key) });
    },
  });
}

/** `PATCH /saved-reports/{id}` with a new name. */
export function useRenameSavedReport(key: ReportKey) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => client.patch(`/saved-reports/${id}`, { name }, { schema: savedReportSchema }),
    onSuccess: async () => {
      toast.success("Đã đổi tên bộ lọc.");
      await queryClient.invalidateQueries({ queryKey: reportKeys.saved(key) });
    },
  });
}

/** `DELETE /saved-reports/{id}`. */
export function useDeleteSavedReport(key: ReportKey) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete(`/saved-reports/${id}`),
    onSuccess: async () => {
      toast.success("Đã xóa bộ lọc.");
      await queryClient.invalidateQueries({ queryKey: reportKeys.saved(key) });
    },
  });
}
