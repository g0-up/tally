// Public surface of the partners feature. Pages are loaded by the router from ./pages; anything other
// features may import is re-exported here.
export { SupplierQuickDialog, type SupplierQuickDialogProps } from "./containers/supplier-quick-dialog";
export { activeSupplierSource, supplierKeys } from "./api/queries";
export type { Supplier, SupplierResult } from "./schemas";
