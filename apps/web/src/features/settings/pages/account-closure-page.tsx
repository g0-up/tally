import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { RequireRecentAuth } from "../../../app/guards/require-recent-auth";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage } from "../../../shared/api/api-error";
import { CommitCancelledError, useCommit } from "../../../shared/hooks/use-commit";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { PageHeader } from "../../../shared/ui/page-header";
import { closureRequestSchema, type ClosureRequest } from "../api/account";
import { settingsKeys } from "../api/tenant-settings";
import { CLOSURE_TEXT } from "../components/account-closure-card";

export const CLOSURE_VERIFY_LABEL = "Xác nhận đóng tài khoản";
export const CLOSURE_TOKEN_MISSING = "Liên kết thiếu mã xác nhận. Hãy mở lại liên kết trong email.";

/** Xác nhận đóng tài khoản (/settings/account-closure?token=…), opened from the e-mailed link. */
export default function AccountClosurePage() {
  const client = useApiClient();
  const [params] = useSearchParams();
  const token = params.get("token")?.trim() ?? "";
  const [done, setDone] = useState<ClosureRequest | null>(null);

  const verify = useCommit({
    mutationFn: (value: string) => client.post("/account-closure:verify", { token: value }, { schema: closureRequestSchema }),
    success: () => "Đã xác nhận đóng tài khoản.",
    invalidate: [settingsKeys.closure()],
    after: setDone,
  });
  // Closing the password prompt is the owner's choice, not a failure to report.
  const failure = verify.error instanceof CommitCancelledError ? null : verify.error;

  return (
    <>
      <PageHeader eyebrow="Thiết lập" title={CLOSURE_VERIFY_LABEL} />
      <Card>
        <p>{CLOSURE_TEXT}</p>
        {token === "" ? (
          <Callout tone="bad" role="alert">
            {CLOSURE_TOKEN_MISSING}
          </Callout>
        ) : done ? (
          <Callout tone="ok" role="status">
            Đã xác nhận. Cửa hàng vẫn hoạt động đến hết ngày {fd(done.waiting_until)}; bạn có thể hủy yêu cầu trước ngày đó trong Tài khoản &amp; bảo mật.
          </Callout>
        ) : (
          <RequireRecentAuth text="Xác nhận đóng tài khoản cần nhập lại mật khẩu.">
            {failure ? (
              <Callout tone="bad" role="alert" style={{ marginBottom: 10 }}>
                {errorMessage(failure)}
              </Callout>
            ) : null}
            <ButtonRow>
              <Button variant="danger" pending={verify.pending} onClick={() => void verify.run(token)}>
                {CLOSURE_VERIFY_LABEL}
              </Button>
            </ButtonRow>
          </RequireRecentAuth>
        )}
        <p style={{ marginTop: 14 }}>
          <Link to="/settings/account">Về Tài khoản &amp; bảo mật</Link>
        </p>
      </Card>
    </>
  );
}
