import { useEffect, useRef, useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Link } from "react-router";
import { useHostContext } from "../../../app/host-context";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { useSignup } from "../api/mutations";
import { FormAlert } from "../components/form-alert";
import { NewPasswordFields } from "../components/new-password-fields";
import { SlugField } from "../components/slug-field";
import { applyAuthError } from "../lib/auth-errors";
import { storeHostSuffix, suggestSlug } from "../lib/slug";
import { signupSchema, type SignupValues } from "../schemas";

const SIGNUP_FIELDS = ["full_name", "store_name", "slug", "email", "phone", "password"] as const;

/**
 * Root host: creates a store and its owner account. The store address follows the store name
 * until the owner edits it. The API answers the same way whether or not the e-mail already has an
 * account (that case gets a mail instead), so success always ends on "check your mail".
 */
export default function SignupPage() {
  const host = useHostContext();
  const signup = useSignup();
  const [formError, setFormError] = useState<string | null>(null);
  const [storeUrl, setStoreUrl] = useState<string | null>(null);
  // The form is replaced on success, taking the focused button with it; focus moves to the new heading.
  const sentHeading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (storeUrl !== null) sentHeading.current?.focus();
  }, [storeUrl]);
  const [sentTo, setSentTo] = useState("");
  // The address stops following the store name once the owner types in it.
  const [slugEdited, setSlugEdited] = useState(false);
  const {
    register,
    control,
    handleSubmit,
    setError,
    setValue,
    formState: { errors },
  } = useForm<SignupValues>({
    resolver: zodResolver(signupSchema),
    defaultValues: { full_name: "", store_name: "", slug: "", email: "", phone: "", password: "", confirm: "" },
  });

  const storeName = register("store_name", {
    onChange: (e: { target: { value: string } }) => {
      if (!slugEdited) setValue("slug", suggestSlug(e.target.value));
    },
  });

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null);
    const email = v.email.trim();
    const phone = v.phone.trim();
    try {
      const result = await signup.mutateAsync({
        full_name: v.full_name.trim(),
        store_name: v.store_name.trim(),
        slug: v.slug.trim(),
        email,
        ...(phone === "" ? {} : { phone }),
        password: v.password,
      });
      setSentTo(email);
      setStoreUrl(result.store_url);
    } catch (e) {
      setFormError(applyAuthError(e, SIGNUP_FIELDS, setError));
    }
  });

  if (storeUrl !== null) {
    return (
      <>
        <h1 ref={sentHeading} tabIndex={-1}>
          Kiểm tra email của bạn
        </h1>
        <Callout tone="ok" role="status">
          Chúng tôi đã gửi thư tới <b>{sentTo}</b>. Mở liên kết trong thư để xác minh email và kích hoạt cửa hàng. Kiểm tra cả thư mục thư rác.
        </Callout>
        <p>
          Địa chỉ cửa hàng: <span className="auth-url">{storeUrl}</span>
        </p>
        <p className="auth-lead">Lưu lại địa chỉ này: bạn đăng nhập tại đó sau khi xác minh email.</p>
      </>
    );
  }

  return (
    <>
      <h1>Đăng ký cửa hàng</h1>
      <p className="auth-lead">Tạo cửa hàng và tài khoản chủ cửa hàng. Mỗi cửa hàng có một địa chỉ riêng để đăng nhập.</p>
      <FormAlert message={formError} />
      <form className="auth-form" noValidate onSubmit={(e) => void onSubmit(e)}>
        <Field label="Họ tên" error={errors.full_name?.message}>
          <Input autoComplete="name" autoFocus invalid={errors.full_name !== undefined} {...register("full_name")} />
        </Field>
        <Field label="Tên cửa hàng" error={errors.store_name?.message}>
          <Input autoComplete="organization" invalid={errors.store_name !== undefined} {...storeName} />
        </Field>
        <Controller
          control={control}
          name="slug"
          render={({ field }) => (
            <SlugField
              value={field.value}
              name={field.name}
              ref={field.ref}
              onBlur={field.onBlur}
              onChange={(slug) => {
                setSlugEdited(true);
                field.onChange(slug);
              }}
              error={errors.slug?.message}
              hostSuffix={storeHostSuffix(host.data?.root_url)}
            />
          )}
        />
        <Field label="Email" hint="Dùng để đăng nhập và nhận thư xác minh." error={errors.email?.message}>
          <Input type="email" autoComplete="email" invalid={errors.email !== undefined} {...register("email")} />
        </Field>
        <Field label="Số điện thoại" hint="Không bắt buộc. Có thể dùng thay email để đăng nhập." error={errors.phone?.message}>
          <Input type="tel" autoComplete="tel" inputMode="tel" invalid={errors.phone !== undefined} {...register("phone")} />
        </Field>
        <NewPasswordFields password={register("password")} confirm={register("confirm")} passwordError={errors.password?.message} confirmError={errors.confirm?.message} />
        <Button variant="primary" type="submit" pending={signup.isPending} pendingLabel="Đang tạo cửa hàng…">
          Tạo cửa hàng
        </Button>
      </form>
      <p className="auth-links">
        <Link to="/find-store">Đã có cửa hàng? Tìm địa chỉ cửa hàng</Link>
      </p>
    </>
  );
}
