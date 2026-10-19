import { useState } from "react";
import { fdt } from "../../../shared/lib/dates";
import { Button } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { Stack } from "../../../shared/ui/grid";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { Timeline } from "../../../shared/ui/timeline";
import { useIncidents } from "../api/queries";
import type { Incident } from "../api/schemas";
import { PagedRows } from "../components/paged-rows";
import { CreateIncidentDialog, UpdateIncidentDialog } from "../containers/incident-dialogs";
import { INCIDENT_IMPACT, INCIDENT_STATUS } from "../lib/labels";

export const INCIDENTS_TITLE = "Sự cố";
export const BANNER_TITLE = "Thông báo đang hiện cho cửa hàng";

/** Text a store sees for an unresolved incident: its title and the latest update. */
function bannerText(incident: Incident): string {
  const latest = incident.updates.reduce<Incident["updates"][number] | undefined>((a, u) => (a === undefined || u.at > a.at ? u : a), undefined);
  return latest ? `${incident.title}: ${latest.message}` : incident.title;
}

/** Sự cố (/admin/incidents): the public status page's incidents and their updates. */
export default function IncidentsPage() {
  const incidents = useIncidents();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Incident | null>(null);
  const list = incidents.items;

  return (
    <>
      <PageHeader
        eyebrow="Quản trị nền tảng"
        title={INCIDENTS_TITLE}
        lead="Sự cố chưa khắc phục hiện trên trang trạng thái; mỗi lần cập nhật thêm một dòng vào lịch sử."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            Công bố sự cố
          </Button>
        }
      />
      <Stack>
        <StoreBanner />
        <PagedRows list={incidents} what="sự cố">
          {list.length === 0 ? <p className="hint">Chưa có sự cố nào.</p> : null}
          {list.map((i) => (
            <Card
              key={i.id}
              title={i.title}
              note={`${INCIDENT_IMPACT[i.impact]} · bắt đầu ${fdt(i.started_at)}${i.resolved_at ? ` · khắc phục ${fdt(i.resolved_at)}` : ""}`}
              actions={
                <>
                  <StatusPill {...INCIDENT_STATUS[i.status]} />
                  <Button size="sm" aria-label={`Cập nhật ${i.title}`} onClick={() => setEditing(i)}>
                    Cập nhật
                  </Button>
                </>
              }
            >
              <Timeline
                items={[...i.updates]
                  .sort((a, b) => b.at.localeCompare(a.at))
                  .map((u, n) => ({
                    key: `${u.at}:${n}`,
                    time: fdt(u.at),
                    content: (
                      <>
                        <b>{INCIDENT_STATUS[u.status].label}</b> · {u.message}
                      </>
                    ),
                  }))}
              />
            </Card>
          ))}
        </PagedRows>
      </Stack>
      {creating ? <CreateIncidentDialog onClose={() => setCreating(false)} /> : null}
      {editing ? <UpdateIncidentDialog incident={editing} onClose={() => setEditing(null)} /> : null}
    </>
  );
}

/** What stores see now: the incidents not resolved yet, read with `status=open`. */
function StoreBanner() {
  const open = useIncidents("open");
  return (
    <Card title={BANNER_TITLE}>
      <PagedRows list={open} what="sự cố đang mở">
        {open.items.length === 0 ? <p className="hint">Không có sự cố đang mở; cửa hàng không thấy thông báo nào.</p> : null}
        {open.items.map((i) => (
          <Callout key={i.id} tone={i.impact === "none" || i.impact === "minor" ? "warn" : "bad"}>
            {bannerText(i)}
          </Callout>
        ))}
      </PagedRows>
    </Card>
  );
}
