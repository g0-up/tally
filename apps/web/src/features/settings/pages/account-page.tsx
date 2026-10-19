import { Grid, Stack } from "../../../shared/ui/grid";
import { PageHeader } from "../../../shared/ui/page-header";
import { AccountClosureCard } from "../components/account-closure-card";
import { DataExportCard } from "../components/data-export-card";
import { DevicesCard } from "../components/devices-card";
import { OfflinePolicyCard } from "../components/offline-policy-card";
import { OwnerCard } from "../components/owner-card";
import { OwnerTransferCard } from "../components/owner-transfer-card";
import { PeriodLockCard } from "../components/period-lock-card";
import { StoreProfileCard } from "../components/store-profile-card";

export const ACCOUNT_LEAD = "Sản phẩm dành cho cửa hàng một người: chỉ có tài khoản chủ cửa hàng, toàn quyền mọi màn hình.";

/** Tài khoản & bảo mật (/settings/account). */
export default function AccountPage() {
  return (
    <>
      <PageHeader lead={ACCOUNT_LEAD} />
      <Grid cols="g2">
        <OwnerCard />
        <DevicesCard />
      </Grid>
      <Stack style={{ marginTop: 20 }}>
        <StoreProfileCard />
        <PeriodLockCard />
        <OfflinePolicyCard />
        <Grid cols="g2">
          <DataExportCard />
          <OwnerTransferCard />
        </Grid>
        <AccountClosureCard />
      </Stack>
    </>
  );
}
