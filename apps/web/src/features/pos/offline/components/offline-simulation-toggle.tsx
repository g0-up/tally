import { Button } from "../../../../shared/ui/button";
import { connectivity } from "../connectivity";

/**
 * Demo and test switch of development builds and sandbox stores: "Mô phỏng mất mạng" forces the
 * offline state, "Kết nối lại mạng" hands it back to the real network check.
 */
export function OfflineSimulationToggle({ simulated }: { simulated: boolean }) {
  return (
    <Button variant="line" onClick={() => connectivity.simulate(!simulated)}>
      {simulated ? "Kết nối lại mạng" : "Mô phỏng mất mạng"}
    </Button>
  );
}
