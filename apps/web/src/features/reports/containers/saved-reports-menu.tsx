import { useState } from "react";
import { fdt } from "../../../shared/lib/dates";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { Field } from "../../../shared/ui/field";
import { Input } from "../../../shared/ui/input";
import { ListLoading } from "../../../shared/ui/list-footer";
import { toast } from "../../../shared/ui/toast-store";
import { REPORT_MESSAGES, REPORT_TEXT, reportErrorMessage } from "../api/adapters";
import { useDeleteSavedReport, useRenameSavedReport, useSaveReport } from "../api/mutations";
import { useSavedReports } from "../api/queries";
import type { ReportConfig, ReportKey, SavedReport } from "../schemas";

/** Longest name the API takes. */
const NAME_MAX = 120;

type Mode = { kind: "save" } | { kind: "list" } | { kind: "rename"; view: SavedReport } | { kind: "remove"; view: SavedReport };

function NameDialog({ title, initial, onClose, onSubmit, pending, error }: { title: string; initial: string; onClose: () => void; onSubmit: (name: string) => void; pending: boolean; error: string | null }) {
  const [name, setName] = useState(initial);
  const [touched, setTouched] = useState(false);
  const blank = name.trim() === "";
  return (
    <CommitDialog
      open
      onClose={onClose}
      title={title}
      submitLabel="Lưu"
      pending={pending}
      onSubmit={() => {
        setTouched(true);
        if (!blank) onSubmit(name.trim());
      }}
    >
      <Field label="Tên bộ lọc" error={touched && blank ? REPORT_MESSAGES.nameRequired : (error ?? undefined)}>
        <Input value={name} maxLength={NAME_MAX} onChange={(e) => setName(e.target.value)} invalid={(touched && blank) || error !== null} />
      </Field>
    </CommitDialog>
  );
}

export interface SavedReportsMenuProps {
  reportKey: ReportKey;
  /** The layout on screen, stored by "Lưu bộ lọc". */
  config: ReportConfig;
  onApply: (config: ReportConfig) => void;
}

/** "Lưu bộ lọc" and "Bộ lọc đã lưu": named layouts of one report, applied back to the URL. */
export function SavedReportsMenu({ reportKey, config, onApply }: SavedReportsMenuProps) {
  const [mode, setMode] = useState<Mode | null>(null);
  const saved = useSavedReports(reportKey, mode?.kind === "list");
  const save = useSaveReport(reportKey);
  const rename = useRenameSavedReport(reportKey);
  const remove = useDeleteSavedReport(reportKey);
  const toList = () => {
    rename.reset();
    remove.reset();
    setMode({ kind: "list" });
  };
  const close = () => {
    save.reset();
    setMode(null);
  };

  const columns: Column<SavedReport>[] = [
    { key: "name", header: "Tên bộ lọc", cell: (v) => v.name },
    { key: "updated", header: "Cập nhật", cell: (v) => fdt(v.updated_at) },
    {
      key: "acts",
      header: <span className="sr-only">Thao tác</span>,
      className: "acts",
      cell: (v) => (
        <ButtonRow>
          <Button
            size="sm"
            variant="primary"
            aria-label={`Áp dụng ${v.name}`}
            onClick={() => {
              onApply(v.config);
              setMode(null);
              toast.success(`Đã áp dụng bộ lọc “${v.name}”.`);
            }}
          >
            Áp dụng
          </Button>
          <Button size="sm" aria-label={`Đổi tên ${v.name}`} onClick={() => setMode({ kind: "rename", view: v })}>
            Đổi tên
          </Button>
          <Button size="sm" variant="danger" aria-label={`Xóa ${v.name}`} onClick={() => setMode({ kind: "remove", view: v })}>
            Xóa
          </Button>
        </ButtonRow>
      ),
    },
  ];

  return (
    <>
      <Button size="sm" onClick={() => setMode({ kind: "save" })}>
        {REPORT_TEXT.save}
      </Button>
      <Button size="sm" onClick={() => setMode({ kind: "list" })}>
        {REPORT_TEXT.saved}
      </Button>
      {mode?.kind === "save" ? (
        <NameDialog
          title={REPORT_TEXT.save}
          initial=""
          onClose={close}
          pending={save.isPending}
          error={save.error ? reportErrorMessage(save.error) : null}
          onSubmit={(name) => save.mutate({ name, config }, { onSuccess: close })}
        />
      ) : null}
      {mode?.kind === "list" ? (
        <CommitDialog open onClose={close} title={REPORT_TEXT.saved} sub="Áp dụng lại cột và cách nhóm đã lưu; khoảng ngày giữ như đang xem." cancelLabel="Đóng">
          {saved.isPending ? (
            <ListLoading />
          ) : saved.isError ? (
            <Callout tone="bad" role="alert">
              {reportErrorMessage(saved.error)}
            </Callout>
          ) : (
            <DataTable caption={REPORT_TEXT.saved} columns={columns} rows={saved.data} rowKey={(v) => v.id} flat empty="Chưa có bộ lọc nào được lưu." />
          )}
        </CommitDialog>
      ) : null}
      {mode?.kind === "rename" ? (
        <NameDialog
          title="Đổi tên bộ lọc"
          initial={mode.view.name}
          onClose={toList}
          pending={rename.isPending}
          error={rename.error ? reportErrorMessage(rename.error) : null}
          onSubmit={(name) => rename.mutate({ id: mode.view.id, name }, { onSuccess: toList })}
        />
      ) : null}
      {mode?.kind === "remove" ? (
        <CommitDialog
          open
          onClose={toList}
          title="Xóa bộ lọc"
          sub={`Xóa bộ lọc “${mode.view.name}”? Báo cáo không đổi, chỉ mất cách xem đã lưu.`}
          submitLabel="Xóa"
          submitVariant="danger"
          pending={remove.isPending}
          onSubmit={() => remove.mutate(mode.view.id, { onSuccess: toList })}
        >
          {remove.error ? (
            <Callout tone="bad" role="alert">
              {reportErrorMessage(remove.error)}
            </Callout>
          ) : null}
        </CommitDialog>
      ) : null}
    </>
  );
}
