import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";

export interface DocNotFoundDrawerProps {
  /** Document kind, the drawer title ("Kiểm kê", "Phiếu điều chỉnh"). */
  title: string;
  docNo: string;
  onClose: () => void;
}

/** A `?doc=` link whose number none of the loaded documents of this screen carries. */
export function DocNotFoundDrawer({ title, docNo, onClose }: DocNotFoundDrawerProps) {
  return (
    <DetailDrawer open onClose={onClose} title={title} docKey={docNo}>
      <DrawerError message={`Không tìm thấy ${docNo}.`} />
    </DetailDrawer>
  );
}
