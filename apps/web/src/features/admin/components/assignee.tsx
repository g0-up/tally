import { useAdminSession } from "../../../shared/api/platform-queries";
import { useAdmins } from "../api/queries";

/**
 * Who a ticket is assigned to, named from the admin directory; the signed-in admin is marked
 * "(bạn)". The id shows only while the directory is loading or could not be read.
 */
export function Assignee({ adminId }: { adminId: string | null }) {
  const me = useAdminSession().data?.admin;
  const admins = useAdmins();
  if (!adminId) return <>Chưa phân công</>;
  if (me && adminId === me.id) return <>{me.full_name} (bạn)</>;
  const admin = admins.data?.find((a) => a.id === adminId);
  if (admin) return <>{admin.active ? admin.full_name : `${admin.full_name} (ngừng hoạt động)`}</>;
  return <code>{adminId}</code>;
}
