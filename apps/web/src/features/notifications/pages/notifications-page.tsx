import { useLayoutEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { focusLost, mainFocusTarget } from "../../../shared/hooks/use-return-focus";
import { Button } from "../../../shared/ui/button";
import { toast } from "../../../shared/ui/toast-store";
import { CellSub } from "../../../shared/ui/data-table/cell-sub";
import { DataTable, type Column } from "../../../shared/ui/data-table/data-table";
import { DocNo } from "../../../shared/ui/doc-no";
import { FilterBar } from "../../../shared/ui/filter-bar";
import { ListFooter, ListLoading } from "../../../shared/ui/list-footer";
import { PageHeader } from "../../../shared/ui/page-header";
import { StatusPill } from "../../../shared/ui/status-pill";
import { ToggleGroup } from "../../../shared/ui/toggle-group";
import { useLoadAll } from "../../inventory";
import { useExportDownload } from "../../reports";
import { useMarkNotificationRead, useNotificationCommand, type NotificationCommandVars } from "../api/mutations";
import { useActiveNotifications } from "../api/queries";
import { SEVERITY_TONE, kindCounts, kindLabel, notificationTarget } from "../kinds";
import type { Notification } from "../schemas";

const ALL = "all";

/** A row's action named with the notification it acts on ("Đã xử lý – Cô Lan quá hạn 2 ngày"). */
const rowAction = (label: string, n: Notification) => `${label} – ${n.title}`;

/**
 * Thông báo & việc cần làm (/overview/notifications): the active notifications, one chip per kind
 * with its count. "Mở" opens the screen that handles a notification and "Tải về" the file of a
 * finished export; either then marks it read. Nothing here creates a document. Every action is
 * named with its notification, only the one running shows progress, and when a handled row leaves
 * the list focus moves to the next row's first action (the page's main region once none is left).
 */
export default function NotificationsPage({ openFile }: { openFile?: (path: string) => void } = {}) {
  const navigate = useNavigate();
  const list = useActiveNotifications();
  // The chip counts need every active notification, not the first page.
  useLoadAll(list);
  const command = useNotificationCommand();
  const markRead = useMarkNotificationRead();
  const file = useExportDownload(openFile);
  const [picked, setPicked] = useState(ALL);
  const [acting, setActing] = useState<NotificationCommandVars | null>(null);
  const [downloading, setDownloading] = useState<string | null>(null);
  const tableRef = useRef<HTMLDivElement>(null);
  // The notification just snoozed or resolved: focus waits until it has left the list.
  const refocus = useRef<{ id: string; leaves: boolean } | null>(null);

  const counts = kindCounts(list.items);
  // A chip whose last notification was handled falls back to "Tất cả".
  const kind = counts.some((c) => c.kind === picked) ? picked : ALL;
  const rows = kind === ALL ? list.items : list.items.filter((n) => n.kind === kind);

  useLayoutEffect(() => {
    const target = refocus.current;
    if (target === null || command.pending) return;
    if (target.leaves && rows.some((n) => n.id === target.id)) return;
    refocus.current = null;
    if (!focusLost()) return;
    const table = tableRef.current;
    const own = target.leaves ? null : table?.querySelector<HTMLElement>(`button[data-notification="${CSS.escape(target.id)}"]:not(:disabled)`);
    (own ?? table?.querySelector<HTMLElement>("tbody button:not(:disabled)") ?? mainFocusTarget())?.focus();
  }, [rows, command.pending]);

  const act = async (vars: NotificationCommandVars) => {
    setActing(vars);
    const done = await command.run(vars);
    refocus.current = { id: vars.id, leaves: done !== undefined };
    setActing(null);
  };

  const open = (n: Notification, to: string) => {
    void navigate(to);
    if (!n.read_at) markRead(n.id);
  };

  const download = async (n: Notification, jobId: string) => {
    setDownloading(n.id);
    const result = await file.download(jobId).finally(() => setDownloading(null));
    if (result.message !== null) toast.error(result.message);
    else if (!n.read_at) markRead(n.id);
  };

  const columns: Column<Notification>[] = [
    { key: "kind", header: "Loại", cell: (n) => <StatusPill label={kindLabel(n.kind)} tone={SEVERITY_TONE[n.severity]} /> },
    {
      key: "content",
      header: "Nội dung",
      cell: (n) => (
        <>
          {n.read_at ? n.title : <b>{n.title}</b>}
          {n.doc_no || n.body ? (
            <CellSub>
              {n.doc_no ? <DocNo value={n.doc_no} /> : null}
              {n.doc_no && n.body ? " · " : null}
              {n.body}
            </CellSub>
          ) : null}
        </>
      ),
    },
    {
      key: "acts",
      header: "",
      className: "acts",
      cell: (n) => {
        const to = notificationTarget(n);
        const exportId = n.kind === "export_ready" ? n.entity_id : null;
        const fetching = file.pending && downloading === n.id;
        const commandButton = (cmd: NotificationCommandVars["command"], label: string) => {
          const running = acting?.id === n.id && acting.command === cmd;
          return (
            <Button
              size="sm"
              data-notification={n.id}
              // The command is single-flight: the other rows wait, only the running one shows progress.
              disabled={command.pending}
              pending={running}
              pendingLabel="Đang lưu…"
              aria-label={running ? undefined : rowAction(label, n)}
              onClick={() => void act({ id: n.id, command: cmd })}
            >
              {label}
            </Button>
          );
        };
        return (
          <>
            {to !== null ? (
              <>
                <Button size="sm" aria-label={rowAction("Mở", n)} onClick={() => open(n, to)}>
                  Mở
                </Button>{" "}
              </>
            ) : null}
            {exportId ? (
              <>
                <Button
                  size="sm"
                  disabled={file.pending}
                  pending={fetching}
                  pendingLabel="Đang mở…"
                  aria-label={fetching ? undefined : rowAction("Tải về", n)}
                  onClick={() => void download(n, exportId)}
                >
                  Tải về
                </Button>{" "}
              </>
            ) : null}
            {commandButton("snooze", "Nhắc lại mai")} {commandButton("resolve", "Đã xử lý")}
          </>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader lead="Thông báo không tự sinh chứng từ: ví dụ cảnh báo thiếu hàng chỉ gợi ý mua, không tự đặt PO." />
      <FilterBar>
        <ToggleGroup
          label="Loại thông báo"
          options={[{ value: ALL, label: `Tất cả (${list.items.length})` }, ...counts.map((c) => ({ value: c.kind, label: `${kindLabel(c.kind)} (${c.count})` }))]}
          value={kind}
          onChange={setPicked}
        />
      </FilterBar>
      {list.isLoading ? (
        <ListLoading />
      ) : (
        <div ref={tableRef}>
          <DataTable caption="Thông báo" columns={columns} rows={rows} rowKey={(n) => n.id} empty="Không có việc cần xử lý." />
        </div>
      )}
      <ListFooter list={list} />
    </>
  );
}
