import { useState } from "react";
import { fd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { Grid } from "../../../shared/ui/grid";
import { ListLoading } from "../../../shared/ui/list-footer";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { ActiveAddon, Plan } from "../../../shared/api/endpoints";
import { usePlans } from "../api/billing";
import { PLAN_DESCRIPTION, PLAN_MODULE, priceText } from "../labels";
import { LoadError } from "./load-error";
import { ADDON_BUY_SUBMIT, PlanChangeDialog } from "./plan-change-dialog";

export const ADDONS_TITLE = "Gói bổ sung";

/** The plan every store starts from. */
const BASE_PLAN_CODE = "store";

function modulesText(modules: readonly string[]): string {
  return modules.map((m) => PLAN_MODULE[m] ?? m).join(", ");
}

function planHint(plan: Plan): string {
  return PLAN_DESCRIPTION[plan.code] ?? modulesText(plan.modules);
}

/**
 * The plans a store can be on (trials excluded), the current one marked, then the add-ons it can
 * buy on top, those in force (`addons` of GET /subscription) marked with their period.
 */
export function PlanCards({ currentPlanId, addons: inForce }: { currentPlanId: string | null; addons: readonly ActiveAddon[] }) {
  const plans = usePlans();
  const [changeTo, setChangeTo] = useState<Plan | null>(null);

  if (plans.isPending) return <ListLoading />;
  if (plans.error) return <LoadError what="danh sách gói" error={plans.error} />;
  const shown = plans.data.filter((p) => p.active && p.kind !== "trial" && p.kind !== "addon");
  const addons = plans.data.filter((p) => p.kind === "addon");

  return (
    <>
      <Grid cols="g4">
        {shown.map((p) => {
          const current = p.id === currentPlanId;
          return (
            <Card
              key={p.id}
              title={p.name}
              actions={current ? <StatusPill label="Đang dùng" /> : p.code === BASE_PLAN_CODE ? <StatusPill label="Gói nền" /> : null}
            >
              <p className="hint">{planHint(p)}</p>
              {current ? null : (
                <ButtonRow style={{ marginTop: 10 }}>
                  <Button size="sm" aria-label={`Đổi sang gói ${p.name}`} onClick={() => setChangeTo(p)}>
                    Đổi sang gói này
                  </Button>
                </ButtonRow>
              )}
            </Card>
          );
        })}
      </Grid>
      <AddonCards addons={addons} inForce={inForce} onBuy={setChangeTo} />
      <PlanChangeDialog plan={changeTo} onClose={() => setChangeTo(null)} />
    </>
  );
}

/**
 * Add-ons: those in force (GET /subscription `addons`, already picked by the store's business date)
 * with their period, the others on sale to buy. An add-on no longer sold but still in force keeps
 * its card, named as the subscription names it.
 */
function AddonCards({ addons, inForce, onBuy }: { addons: readonly Plan[]; inForce: readonly ActiveAddon[]; onBuy: (plan: Plan) => void }) {
  const terms = new Map(inForce.map((a) => [a.plan_id, a]));
  // GET /plans lists only plans on sale.
  const listed = addons.filter((p) => p.active);
  const retired = inForce.filter((a) => !listed.some((p) => p.id === a.plan_id));
  if (listed.length === 0 && retired.length === 0) return null;

  return (
    <section aria-label={ADDONS_TITLE} style={{ marginTop: 20 }}>
      <h3 className="sec-h">{ADDONS_TITLE}</h3>
      <Grid cols="g4">
        {listed.map((p) => {
          const term = terms.get(p.id);
          return (
            <Card key={p.id} title={p.name} actions={term ? <StatusPill label="Đang dùng" /> : null}>
              <p className="hint">{planHint(p)}</p>
              <p className="hint">Giá: {priceText(p)}</p>
              {term ? (
                <AddonTerm term={term} />
              ) : (
                <ButtonRow style={{ marginTop: 10 }}>
                  <Button size="sm" aria-label={`${ADDON_BUY_SUBMIT} ${p.name}`} onClick={() => onBuy(p)}>
                    {ADDON_BUY_SUBMIT}
                  </Button>
                </ButtonRow>
              )}
            </Card>
          );
        })}
        {retired.map((a) => (
          <Card key={a.plan_id} title={a.plan_name} actions={<StatusPill label="Đang dùng" />}>
            <p className="hint">{modulesText(a.modules)}</p>
            <AddonTerm term={a} />
          </Card>
        ))}
      </Grid>
    </section>
  );
}

function AddonTerm({ term }: { term: ActiveAddon }) {
  return (
    <p className="hint">
      Hiệu lực {fd(term.period_start)} – {fd(term.period_end)}
    </p>
  );
}
