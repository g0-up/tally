import { useState } from "react";
import { CommitDialog } from "../../../shared/ui/commit-dialog";
import { Field } from "../../../shared/ui/field";
import { FormGrid } from "../../../shared/ui/form-grid";
import { Input, Textarea } from "../../../shared/ui/input";
import { useCreateIncident, useUpdateIncident, type UpdateIncidentBody } from "../api/mutations";
import type { Incident, IncidentImpact, IncidentStatus } from "../api/schemas";
import { INCIDENT_IMPACT, INCIDENT_STATUS, entriesOf } from "../lib/labels";

function StatusSelect({ value, onChange }: { value: IncidentStatus; onChange: (v: IncidentStatus) => void }) {
  return (
    <select className="in" value={value} onChange={(e) => onChange(e.target.value as IncidentStatus)}>
      {entriesOf(INCIDENT_STATUS).map(([v, s]) => (
        <option key={v} value={v}>
          {s.label}
        </option>
      ))}
    </select>
  );
}

function ImpactSelect({ value, onChange }: { value: IncidentImpact; onChange: (v: IncidentImpact) => void }) {
  return (
    <select className="in" value={value} onChange={(e) => onChange(e.target.value as IncidentImpact)}>
      {entriesOf(INCIDENT_IMPACT).map(([v, label]) => (
        <option key={v} value={v}>
          {label}
        </option>
      ))}
    </select>
  );
}

export function CreateIncidentDialog({ onClose }: { onClose: () => void }) {
  const [title, setTitle] = useState("");
  const [impact, setImpact] = useState<IncidentImpact>("minor");
  const [status, setStatus] = useState<IncidentStatus>("investigating");
  const [message, setMessage] = useState("");
  const [errors, setErrors] = useState<{ title?: string | undefined; message?: string | undefined }>({});
  const create = useCreateIncident({
    success: (i) => (
      <>
        Đã công bố sự cố <b>{i.title}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    const next = { title: title.trim() ? undefined : "Nhập tiêu đề sự cố.", message: message.trim() ? undefined : "Nhập thông báo đầu tiên." };
    setErrors(next);
    if (next.title || next.message) return;
    void create.run({ title: title.trim(), impact, status, message: message.trim() });
  };
  return (
    <CommitDialog open onClose={onClose} title="Công bố sự cố" sub="Sự cố chưa khắc phục hiện trên trang trạng thái của mọi cửa hàng." onSubmit={submit} submitLabel="Công bố" pending={create.pending}>
      <Field label="Tiêu đề" error={errors.title}>
        <Input value={title} invalid={errors.title !== undefined} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <FormGrid>
        <Field label="Mức ảnh hưởng">
          <ImpactSelect value={impact} onChange={setImpact} />
        </Field>
        <Field label="Trạng thái">
          <StatusSelect value={status} onChange={setStatus} />
        </Field>
      </FormGrid>
      <Field label="Thông báo" error={errors.message}>
        <Textarea rows={3} value={message} invalid={errors.message !== undefined} onChange={(e) => setMessage(e.target.value)} />
      </Field>
    </CommitDialog>
  );
}

export function UpdateIncidentDialog({ incident, onClose }: { incident: Incident; onClose: () => void }) {
  const [status, setStatus] = useState<IncidentStatus>(incident.status);
  const [impact, setImpact] = useState<IncidentImpact>(incident.impact);
  const [message, setMessage] = useState("");
  const [error, setError] = useState<string>();
  const update = useUpdateIncident(incident.id, {
    success: (i) => (
      <>
        Đã cập nhật sự cố <b>{i.title}</b>.
      </>
    ),
    after: onClose,
  });
  const submit = () => {
    const body: UpdateIncidentBody = {};
    if (status !== incident.status) body.status = status;
    if (impact !== incident.impact) body.impact = impact;
    if (message.trim()) body.message = message.trim();
    if (Object.keys(body).length === 0) return setError("Đổi trạng thái, mức ảnh hưởng hoặc nhập thông báo mới.");
    void update.run(body);
  };
  return (
    <CommitDialog open onClose={onClose} title={`Cập nhật sự cố · ${incident.title}`} onSubmit={submit} submitLabel="Cập nhật" pending={update.pending}>
      <FormGrid>
        <Field label="Trạng thái">
          <StatusSelect
            value={status}
            onChange={(v) => {
              setStatus(v);
              setError(undefined);
            }}
          />
        </Field>
        <Field label="Mức ảnh hưởng">
          <ImpactSelect
            value={impact}
            onChange={(v) => {
              setImpact(v);
              setError(undefined);
            }}
          />
        </Field>
      </FormGrid>
      <Field label="Thông báo mới" error={error}>
        <Textarea
          rows={3}
          value={message}
          onChange={(e) => {
            setMessage(e.target.value);
            setError(undefined);
          }}
        />
      </Field>
    </CommitDialog>
  );
}
