import { createContext, createElement, useContext, type ReactNode } from "react";
import type { HttpClient } from "./http";

// The HTTP client is created once per host kind by the app and handed down through context, so
// hooks and feature adapters never build URLs or clients themselves.

export const ApiClientContext = createContext<HttpClient | null>(null);

export function ApiClientProvider({ client, children }: { client: HttpClient; children: ReactNode }) {
  return createElement(ApiClientContext.Provider, { value: client }, children);
}

export function useApiClient(): HttpClient {
  const client = useContext(ApiClientContext);
  if (!client) throw new Error("useApiClient must be used inside ApiClientProvider");
  return client;
}
