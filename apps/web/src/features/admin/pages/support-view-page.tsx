import { useEffect, useReducer, useState, type FormEvent } from "react";
import { useParams, useSearchParams } from "react-router";
import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { fdt } from "../../../shared/lib/dates";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { Field } from "../../../shared/ui/field";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { Input } from "../../../shared/ui/input";
import { TextLink } from "../../../shared/ui/link-button";
import { ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { useSupportSessions, useSupportView } from "../api/queries";
import type { SupportModule } from "../api/schemas";
import { LoadError } from "../components/load-error";
import { SUPPORT_MODULE } from "../lib/labels";
import { activeGrant, defaultSupportPath, isSupportModule, msUntilExpiry, supportPathError } from "../lib/support-view";

export const SUPPORT_VIEW_NO_GRANT = "Chưa có phiên hỗ trợ đang hiệu lực cho mục này. Chủ cửa hàng cần cấp phiên hỗ trợ trước khi bạn xem được dữ liệu.";

/**
 * Xem dữ liệu cửa hàng (/admin/support/tenants/:tenantId/:module): one store API read through the
 * admin's support session, shown read-only as the JSON the store API returns. Nothing is asked of
 * the API until the admin holds an active session covering the module.
 */
export default function SupportViewPage() {
  const { tenantId = "", module = "" } = useParams();
  if (!isSupportModule(module)) {
    return (
      <>
        <PageHeader eyebrow="Hỗ trợ" title="Xem dữ liệu cửa hàng" actions={<TextLink to="/admin/support">Về hỗ trợ</TextLink>} />
        <Callout tone="warn">Mục dữ liệu không tồn tại.</Callout>
      </>
    );
  }
  return <SupportView tenantId={tenantId} module={module} />;
}

function SupportView({ tenantId, module }: { tenantId: string; module: SupportModule }) {
  const [params, setParams] = useSearchParams();
  const path = (params.get("path") ?? defaultSupportPath(module)).replace(/^\/+/, "");
  const [draft, setDraft] = useState(path);
  const [draftError, setDraftError] = useState<string>();
  const sessions = useSupportSessions();
  const grant = sessions.data ? activeGrant(sessions.data, tenantId, module) : undefined;
  // Read the grant again when it runs out, so the store's data leaves the screen on time.
  const [, recheck] = useReducer((n: number) => n + 1, 0);
  const expiresAt = grant?.expires_at;
  useEffect(() => {
    if (!expiresAt) return;
    const id = setTimeout(recheck, msUntilExpiry(expiresAt));
    return () => clearTimeout(id);
  }, [expiresAt]);
  const pathError = supportPathError(module, path);
  const view = useSupportView(tenantId, module, path, grant !== undefined && pathError === null);

  const go = (e: FormEvent) => {
    e.preventDefault();
    const next = draft.trim().replace(/^\/+/, "");
    const err = supportPathError(module, next);
    if (err) return setDraftError(err);
    setParams({ path: next });
  };

  const inactive = view.isError && isApiError(view.error) && view.error.code === "support_session_inactive";

  return (
    <>
      <PageHeader
        eyebrow="Hỗ trợ · chỉ xem"
        title={`Xem dữ liệu cửa hàng · ${SUPPORT_MODULE[module]}`}
        lead={grant ? `Phiên hỗ trợ hết hạn lúc ${fdt(grant.expires_at)}.` : undefined}
        actions={<TextLink to="/admin/support">Về hỗ trợ</TextLink>}
      />
      {sessions.isError ? <LoadError what="phiên hỗ trợ" error={sessions.error} onRetry={() => void sessions.refetch()} /> : null}
      {sessions.isPending ? (
        <ListLoading />
      ) : !sessions.data ? null : grant === undefined ? (
        <Callout tone="warn" role="status">
          {SUPPORT_VIEW_NO_GRANT}
        </Callout>
      ) : (
        <>
          <form onSubmit={go}>
            <FilterBar>
              <Field label="Đường dẫn" error={draftError} inline>
                <Input
                  value={draft}
                  invalid={draftError !== undefined}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => {
                    setDraft(e.target.value);
                    setDraftError(undefined);
                  }}
                />
              </Field>
              <Button type="submit">Xem</Button>
            </FilterBar>
          </form>
          {pathError ? (
            <Callout tone="warn">{pathError}</Callout>
          ) : inactive ? (
            <Callout tone="warn" role="status">
              Phiên hỗ trợ đã hết hạn hoặc đã bị thu hồi. Chủ cửa hàng cần cấp lại phiên hỗ trợ.
            </Callout>
          ) : view.isError ? (
            <Callout tone="bad" role="alert">
              Không đọc được dữ liệu: {errorMessage(view.error)}
            </Callout>
          ) : view.isPending ? (
            <ListLoading />
          ) : (
            <Card title={`/${path}`} note="Dữ liệu gốc từ cửa hàng, chỉ xem">
              <pre className="hint" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", maxHeight: "70vh", overflow: "auto" }}>
                {JSON.stringify(view.data, null, 2)}
              </pre>
            </Card>
          )}
        </>
      )}
    </>
  );
}
