import { Link } from "react-router";
import { useStoreLookup } from "../api/mutations";
import { EmailRequestForm } from "../components/email-request-form";

/** Root host: mails the addresses of the stores an e-mail belongs to, if any. */
export default function FindStorePage() {
  const lookup = useStoreLookup();
  return (
    <>
      <h1>Tìm cửa hàng của bạn</h1>
      <p className="auth-lead">Quên địa chỉ cửa hàng? Nhập email bạn dùng để đăng nhập, chúng tôi sẽ gửi danh sách địa chỉ cửa hàng tới email đó.</p>
      <EmailRequestForm
        send={(email) => lookup.mutateAsync({ email })}
        pending={lookup.isPending}
        submitLabel="Gửi địa chỉ cửa hàng"
        pendingLabel="Đang gửi…"
        done={(email) => (
          <>
            Nếu <b>{email}</b> thuộc một cửa hàng trên hệ thống, thư kèm địa chỉ cửa hàng sẽ đến trong vài phút. Kiểm tra cả thư mục thư rác.
          </>
        )}
      />
      <p className="auth-links">
        <Link to="/signup">Đăng ký cửa hàng mới</Link>
      </p>
    </>
  );
}
