import { Link, isRouteErrorResponse, useRouteError } from "react-router";
import { errorMessage } from "../shared/api/api-error";
import { Button } from "../shared/ui/button";
import { Callout } from "../shared/ui/callout";
import { BRAND_NAME, BRAND_TAGLINE } from "./layouts/brand";

export const LOADING_TEXT = "Đang tải…";
export const NOT_FOUND_TITLE = "Không tìm thấy trang";

/** Prototype render() catch: the message only, never a stack trace or request data. */
export function ScreenError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <Callout tone="bad" role="alert">
      Không hiển thị được màn hình: {errorMessage(error)}
      {onRetry ? (
        <>
          {" "}
          <Button size="sm" onClick={onRetry}>
            Thử lại
          </Button>
        </>
      ) : null}
    </Callout>
  );
}

/** Placeholder while a lazy page or a guard query loads. */
export function RouteLoading() {
  return (
    <div className="card" role="status" aria-live="polite">
      {LOADING_TEXT}
    </div>
  );
}

/** Route `errorElement`: a page that throws renders the callout inside the shell, the shell keeps working. */
export function RouteErrorBoundary() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) return <NotFoundPage />;
  const cause = isRouteErrorResponse(error) ? new Error(error.statusText || String(error.status)) : error;
  return <ScreenError error={cause} />;
}

/** Any path outside the host's route tree. */
export function NotFoundPage() {
  return (
    <div className="auth">
      <div className="auth-card">
        <div className="auth-brand">
          <b>{BRAND_NAME}</b>
          <span>{BRAND_TAGLINE}</span>
        </div>
        <div className="auth-body">
          <h1>{NOT_FOUND_TITLE}</h1>
          <p>Địa chỉ này không có trên cửa hàng. Kiểm tra lại đường dẫn.</p>
          <p>
            <Link to="/">Về trang đầu</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
