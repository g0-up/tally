import { useState } from "react";
import { CLOSE_UNSETTLED_MESSAGE } from "../../../shared/hooks/create-once";
import { useFocusFirstInvalid } from "../../../shared/hooks/use-focus-first-invalid";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { useSaveCustomer } from "../api/mutations";
import { useConsents, useCustomerGroups, usePriceLists } from "../api/queries";
import { CustomerFormFields } from "../components/customer-form";
import { EMPTY_CUSTOMER, customerDraftOf, firstCustomerError, marketingChannels, validateCustomerDraft, type CustomerDraft, type CustomerErrors } from "../customer-form-values";
import type { ConsentChannel, Customer, CustomerGroup } from "../schemas";

const EDIT_SUB = "Đổi số ngày nợ chỉ áp dụng cho đơn bán mới; nợ đã ghi giữ hạn cũ.";
const NEW_SUB = "Mã được tạo tự động. Chỉ tên là bắt buộc.";

export interface CustomerDialogProps {
  /** Null adds a customer. */
  customer: Customer | null;
  onClose: () => void;
}

/** "Thêm khách hàng" / "Sửa khách hàng". Mount per opening. */
export function CustomerDialog({ customer, onClose }: CustomerDialogProps) {
  const groups = useCustomerGroups();
  const consents = useConsents(customer?.id ?? null);
  const title = customer ? `Sửa khách hàng · ${customer.name}` : "Thêm khách hàng";
  if (groups.isPending || (customer !== null && consents.isPending)) {
    return (
      <CommitDialog open onClose={onClose} title={title}>
        <ListLoading />
      </CommitDialog>
    );
  }
  return <CustomerForm customer={customer} groups={groups.data ?? []} granted={marketingChannels(consents.data?.current)} title={title} onClose={onClose} />;
}

/** The built-in retail group, where the server puts a customer without a group; "" when not listed. */
function retailGroupId(groups: readonly CustomerGroup[]): string {
  return groups.find((g) => g.is_system && g.base_price_kind === "retail")?.id ?? "";
}

function CustomerForm({ customer, groups, granted, title, onClose }: CustomerDialogProps & { groups: readonly CustomerGroup[]; granted: readonly ConsentChannel[]; title: string }) {
  const priceLists = usePriceLists();
  const [draft, setDraft] = useState<CustomerDraft>(customer ? customerDraftOf(customer, granted) : { ...EMPTY_CUSTOMER, groupId: retailGroupId(groups) });
  const [errors, setErrors] = useState<CustomerErrors>({});
  const focusFirstInvalid = useFocusFirstInvalid();
  const save = useSaveCustomer({
    customer,
    granted,
    setErrors,
    success: (c) =>
      customer ? (
        "Đã lưu khách hàng."
      ) : (
        <>
          Đã thêm <b>{c.name}</b>. Có thể chọn ngay khi tạo đơn bán.
        </>
      ),
    after: onClose,
  });

  const submit = () => {
    const failed = validateCustomerDraft(draft, granted);
    const first = firstCustomerError(failed);
    setErrors(failed);
    if (first !== undefined) {
      toast.commitError(first);
      // The messages sit under the fields; focus goes to the first one marked.
      focusFirstInvalid();
      return;
    }
    void save.run(draft);
  };

  return (
    <CommitDialog
      open
      onClose={onClose}
      title={title}
      sub={customer ? EDIT_SUB : NEW_SUB}
      submitLabel={customer ? "Lưu" : "Thêm khách hàng"}
      onSubmit={submit}
      pending={save.pending}
      {...(save.unsettled ? { closeWarning: CLOSE_UNSETTLED_MESSAGE } : {})}
    >
      <CustomerFormFields
        draft={draft}
        onChange={(patch) => setDraft((d) => ({ ...d, ...patch }))}
        errors={errors}
        groups={groups}
        priceLists={priceLists.data ?? []}
        customerId={customer?.id ?? null}
      />
    </CommitDialog>
  );
}
