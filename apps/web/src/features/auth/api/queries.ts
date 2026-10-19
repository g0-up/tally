import { useQuery } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { useDebounce } from "../../../shared/hooks/use-debounce";
import { isSlugFormatValid } from "../lib/slug";
import { AUTH_PATHS, slugCheckSchema } from "./contract";

export const authKeys = {
  all: ["auth"] as const,
  slugCheck: (slug: string) => ["auth", "slug-check", slug] as const,
};

/** Typing pause before the store address is checked on the server. */
export const SLUG_CHECK_DEBOUNCE_MS = 300;

/**
 * Live availability of a store address (GET /signup/slug-check), asked once typing pauses and only
 * for addresses whose shape is already valid; the shape itself is checked locally.
 */
export function useSlugCheck(slug: string) {
  const client = useApiClient();
  const debounced = useDebounce(slug, SLUG_CHECK_DEBOUNCE_MS);
  const enabled = debounced === slug && isSlugFormatValid(debounced);
  const query = useQuery({
    queryKey: authKeys.slugCheck(debounced),
    queryFn: ({ signal }) => client.get(AUTH_PATHS.slugCheck, { query: { slug: debounced }, schema: slugCheckSchema, signal, silent: true }),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
  return { ...query, checking: isSlugFormatValid(slug) && (!enabled || query.isFetching) };
}
