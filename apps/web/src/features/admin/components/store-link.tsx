import { useHostContext } from "../../../app/host-context";
import { storeUrl } from "../lib/store-url";

/** The store's own address, opened in a new tab; the slug alone until the root domain is known. */
export function StoreLink({ slug }: { slug: string }) {
  const host = useHostContext();
  const rootUrl = host.data?.root_url;
  const url = rootUrl ? storeUrl(slug, rootUrl) : null;
  if (url === null) return <span>{slug}</span>;
  return (
    <a href={url} target="_blank" rel="noopener noreferrer">
      {url} <span className="sr-only">(mở tab mới)</span>
    </a>
  );
}
