import { useState } from "react";
import { Checkbox } from "../../../shared/ui/checkbox";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { LIST_LOADING_TEXT } from "../../../shared/ui/list-footer";
import { useSetFeatureFlag } from "../api/mutations";
import { useFeatureFlags } from "../api/queries";
import { TenantPicker, type PickedTenant } from "../components/tenant-picker";
import { onOff } from "../lib/flags";

export interface FlagStore {
  id: string;
  slug: string;
}

/**
 * Sets a flag value (PUT /feature-flags): the global one, or one store's when `store` is given or
 * picked. The key is chosen among the keys the server reads (`known_keys`).
 */
export function SetFlagDialog({ store, onClose }: { store?: FlagStore; onClose: () => void }) {
  const flags = useFeatureFlags();
  const [key, setKey] = useState("");
  const [picked, setPicked] = useState<PickedTenant | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [error, setError] = useState<string>();
  const scope = store ?? picked;
  const knownKeys = [...(flags.data?.known_keys ?? [])].sort((a, b) => a.localeCompare(b));
  const set = useSetFeatureFlag({
    success: (f) => (
      <>
        Đã {onOff(f.enabled).toLowerCase()} cờ <b>{f.key}</b> {f.tenant_id ? `cho ${scope?.slug ?? "cửa hàng"}` : "cho toàn hệ thống"}.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    if (key === "") return setError("Chọn mã cờ.");
    void set.run({ key, tenant_id: scope?.id ?? null, enabled });
  };
  return (
    <CommitDialog open onClose={onClose} title={store ? `Đặt cờ cho ${store.slug}` : "Đặt cờ tính năng"} onSubmit={submit} submitLabel="Lưu cờ" pending={set.pending}>
      <Field label="Mã cờ" error={error}>
        <select
          className="in"
          value={key}
          aria-invalid={error !== undefined || undefined}
          onChange={(e) => {
            setKey(e.target.value);
            setError(undefined);
          }}
        >
          <option value="">{flags.isPending ? LIST_LOADING_TEXT : "Chọn mã cờ"}</option>
          {knownKeys.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      </Field>
      {store ? null : (
        <Field label="Phạm vi" hint="Giá trị riêng của một cửa hàng được ưu tiên hơn giá trị chung.">
          <TenantPicker aria-label="Phạm vi" noneLabel="Toàn hệ thống" value={picked} onChange={setPicked} />
        </Field>
      )}
      <Checkbox label="Bật" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
    </CommitDialog>
  );
}
