import { errorMessage } from "../../../shared/api/api-error";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";

export interface DocNotFoundDrawerProps {
  /** Document kind, the drawer title ("Đơn nhập", "Phiếu nhận"). */
  title: string;
  docNo: string;
  /** The lookup failed rather than finding nothing. */
  error?: unknown;
  onClose: () => void;
}

/** A `?doc=` link whose number no document of this screen carries, or whose lookup failed. */
export function DocNotFoundDrawer({ title, docNo, error, onClose }: DocNotFoundDrawerProps) {
  return (
    <DetailDrawer open onClose={onClose} title={title} docKey={docNo}>
      <DrawerError message={error ? errorMessage(error) : `Không tìm thấy ${docNo}.`} />
    </DetailDrawer>
  );
}
