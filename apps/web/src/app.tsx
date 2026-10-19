import { useState } from "react";
import { RouterProvider } from "react-router";
import { QueryProvider } from "./app/providers/query-provider";
import { createAppRouter } from "./app/router";
import type { Platform } from "./app/platform";

/** The SPA for one host kind: its query cache, HTTP client and route tree. */
export function App({ platform }: { platform: Platform }) {
  const [router] = useState(() => createAppRouter(platform.kind));
  return (
    <QueryProvider queryClient={platform.queryClient} client={platform.client}>
      <RouterProvider router={router} />
    </QueryProvider>
  );
}
