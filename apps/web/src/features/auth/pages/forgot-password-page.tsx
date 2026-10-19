import { Link } from "react-router";
import { useRequestPasswordReset } from "../api/mutations";
import { EmailRequestForm } from "../components/email-request-form";

/** Store host: mails a password-reset link when the e-mail has an account in this store. */
export default function ForgotPasswordPage() {
  const request = useRequestPasswordReset();
  return (
    <>
      <h1>Quên mật khẩu</h1>
      <p className="auth-lead">Nhập email đăng nhập. Nếu email có tài khoản ở cửa hàng này, chúng tôi sẽ gửi liên kết đặt lại mật khẩu.</p>
      <EmailRequestForm
        send={(email) => request.mutateAsync({ email })}
        pending={request.isPending}
        submitLabel="Gửi liên kết đặt lại"
        pendingLabel="Đang gửi…"
        done={(email) => (
          <>
            Nếu <b>{email}</b> có tài khoản ở cửa hàng này, liên kết đặt lại mật khẩu sẽ đến trong vài phút. Liên kết dùng được một lần. Kiểm tra cả thư mục thư rác.
          </>
        )}
      />
      <p className="auth-links">
        <Link to="/login">Về trang đăng nhập</Link>
      </p>
    </>
  );
}
