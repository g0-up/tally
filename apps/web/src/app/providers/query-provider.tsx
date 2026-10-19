import type { ReactNode } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { ApiClientProvider } from "../../shared/api/api-context";
import type { HttpClient } from "../../shared/api/http";

/** Server state for one host kind: its QueryClient (memory only) and its HTTP client. */
export function QueryProvider({ queryClient, client, children }: { queryClient: QueryClient; client: HttpClient; children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ApiClientProvider client={client}>{children}</ApiClientProvider>
    </QueryClientProvider>
  );
}
