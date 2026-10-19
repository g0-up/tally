package partner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

const partyColumns = `id, kind, name, name_key, phone, phone_key, email, tax_code, address, note, search_norm,
	created_at, updated_at, version`

const groupColumns = `id, name, name_key, base_price_kind, is_system, active, created_at, updated_at`

const contactColumns = `id, party_id, name, phone, email, role_title, is_primary, created_at, updated_at`

const addressColumns = `id, party_id, label, address_line, is_default, created_at`

// prefixed qualifies every column of a column list with alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// readParty reads a party; forUpdate serialises changes of the party and its profiles.
func readParty(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Party, error) {
	query := `SELECT ` + partyColumns + ` FROM parties WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var p Party
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&p); err != nil {
		if db.IsNoRows(err) {
			return Party{}, errNotFound()
		}
		return Party{}, fmt.Errorf("partner: read party: %w", err)
	}
	return p, nil
}

func insertParty(ctx context.Context, q db.Querier, tenantID uuid.UUID, p Party) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO parties (id, tenant_id, kind, name, name_key, phone, phone_key, email, tax_code, address, note,
		                     search_norm, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, 1)`,
		p.ID, tenantID, p.Kind, p.Name, p.NameKey, p.Phone, p.PhoneKey, p.Email, p.TaxCode, p.Address, p.Note,
		p.SearchNorm, p.CreatedAt); err != nil {
		return fmt.Errorf("partner: insert party: %w", err)
	}
	return nil
}

// updateParty writes the party fields under the optimistic version expected and returns the new
// version. It also runs when only a profile changed, so the party version (the ETag of customer
// and supplier) moves with every change.
func updateParty(ctx context.Context, q db.Querier, p Party, expected int, now time.Time) (int, error) {
	return db.UpdateWithVersion(ctx, q, "parties", p.ID, expected, now,
		db.Set{Column: "kind", Value: p.Kind}, db.Set{Column: "name", Value: p.Name},
		db.Set{Column: "name_key", Value: p.NameKey}, db.Set{Column: "phone", Value: p.Phone},
		db.Set{Column: "phone_key", Value: p.PhoneKey}, db.Set{Column: "email", Value: p.Email},
		db.Set{Column: "tax_code", Value: p.TaxCode}, db.Set{Column: "address", Value: p.Address},
		db.Set{Column: "note", Value: p.Note}, db.Set{Column: "search_norm", Value: p.SearchNorm})
}

// suppliedGoodsNote is the supplier note of a party, nil when it is not a supplier; it is part
// of the party's search text.
func suppliedGoodsNote(ctx context.Context, q db.Querier, partyID uuid.UUID) (*string, error) {
	var note *string
	err := q.QueryRowxContext(ctx, `SELECT supplied_goods_note FROM supplier_profiles WHERE party_id = $1`, partyID).Scan(&note)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("partner: read supplied goods: %w", err)
	}
	return note, nil
}

// customerRow is one customer as the customer queries return it.
type customerRow struct {
	Party
	CustomerGroupID  uuid.UUID    `db:"customer_group_id"`
	CreditLimit      *money.Money `db:"credit_limit"`
	PaymentTermsDays int          `db:"payment_terms_days"`
	Status           string       `db:"status"`
	ProfileCreatedAt time.Time    `db:"profile_created_at"`
	ProfileUpdatedAt time.Time    `db:"profile_updated_at"`
	ProfileVersion   int          `db:"profile_version"`
	GroupName        string       `db:"group_name"`
	IsSupplier       bool         `db:"is_supplier"`
}

func (r customerRow) customer() Customer {
	return Customer{
		Party: r.Party,
		Profile: CustomerProfile{
			PartyID: r.ID, CustomerGroupID: r.CustomerGroupID, CreditLimit: r.CreditLimit,
			PaymentTermsDays: r.PaymentTermsDays, Status: r.Status, CreatedAt: r.ProfileCreatedAt,
			UpdatedAt: r.ProfileUpdatedAt, Version: r.ProfileVersion,
		},
		GroupName: r.GroupName, IsSupplier: r.IsSupplier,
	}
}

var customerSelect = `SELECT ` + prefixed("p", partyColumns) + `,
	       c.customer_group_id, c.credit_limit, c.payment_terms_days, c.status,
	       c.created_at AS profile_created_at, c.updated_at AS profile_updated_at, c.version AS profile_version,
	       g.name AS group_name,
	       EXISTS (SELECT 1 FROM supplier_profiles s WHERE s.party_id = p.id) AS is_supplier
	FROM parties p
	JOIN customer_profiles c ON c.party_id = p.id
	JOIN customer_groups g ON g.id = c.customer_group_id`

// customerListSelect adds the CustomerActivity columns to customerSelect; the consents travel as
// one json array so the list stays a single query.
var customerListSelect = strings.Replace(customerSelect, `
	FROM parties p`, `,
	       (SELECT max(d.posted_at) FROM sales_documents d
	        WHERE d.customer_party_id = p.id AND d.reversal_of IS NULL AND d.status <> 'reversed') AS last_purchase_at,
	       (SELECT COALESCE(jsonb_agg(jsonb_build_object('channel', k.channel, 'purpose', k.purpose, 'granted', k.granted,
	                'recorded_at', k.recorded_at) ORDER BY k.channel, k.purpose), '[]'::jsonb)
	        FROM (SELECT DISTINCT ON (x.channel, x.purpose) x.channel, x.purpose, x.granted, x.recorded_at
	              FROM consents x WHERE x.party_id = p.id
	              ORDER BY x.channel, x.purpose, x.recorded_at DESC, x.id DESC) k) AS consents
	FROM parties p`, 1)

// customerListRow is one row of customerListSelect.
type customerListRow struct {
	customerRow
	LastPurchaseAt *time.Time `db:"last_purchase_at"`
	Consents       []byte     `db:"consents"`
}

func readCustomer(ctx context.Context, q db.Querier, id uuid.UUID) (Customer, error) {
	var r customerRow
	if err := q.QueryRowxContext(ctx, customerSelect+` WHERE p.id = $1`, id).StructScan(&r); err != nil {
		if db.IsNoRows(err) {
			return Customer{}, errNotFound()
		}
		return Customer{}, fmt.Errorf("partner: read customer: %w", err)
	}
	return r.customer(), nil
}

// CustomerFilter selects customers; AfterKey/AfterID continue after a (name_key, id) cursor.
type CustomerFilter struct {
	Q        string
	Status   string
	GroupID  *uuid.UUID
	AfterKey *string
	AfterID  uuid.UUID
	Limit    int
}

func listCustomers(ctx context.Context, q db.Querier, f CustomerFilter, patterns []string) ([]Customer, error) {
	afterKey := ""
	if f.AfterKey != nil {
		afterKey = *f.AfterKey
	}
	var rows []customerListRow
	if err := sqlx.SelectContext(ctx, q, &rows, customerListSelect+`
		WHERE ($1 = '' OR c.status = $1)
		  AND ($2::uuid IS NULL OR c.customer_group_id = $2)
		  AND (cardinality($3::text[]) = 0 OR p.search_norm LIKE ALL ($3::text[]))
		  AND (NOT $4 OR (p.name_key, p.id) > ($5, $6::uuid))
		ORDER BY p.name_key, p.id
		LIMIT $7`, f.Status, f.GroupID, patterns, f.AfterKey != nil, afterKey, f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("partner: list customers: %w", err)
	}
	out := make([]Customer, len(rows))
	for i, r := range rows {
		a := &CustomerActivity{LastPurchaseAt: r.LastPurchaseAt}
		if err := json.Unmarshal(r.Consents, &a.Consents); err != nil {
			return nil, fmt.Errorf("partner: decode customer consents: %w", err)
		}
		out[i] = r.customer()
		out[i].Activity = a
	}
	return out, nil
}

// supplierRow is one supplier as the supplier queries return it.
type supplierRow struct {
	Party
	PaymentTermsDays  int       `db:"payment_terms_days"`
	SuppliedGoodsNote *string   `db:"supplied_goods_note"`
	Status            string    `db:"status"`
	ProfileCreatedAt  time.Time `db:"profile_created_at"`
	ProfileUpdatedAt  time.Time `db:"profile_updated_at"`
	ProfileVersion    int       `db:"profile_version"`
	IsCustomer        bool      `db:"is_customer"`
}

func (r supplierRow) supplier() Supplier {
	return Supplier{
		Party: r.Party,
		Profile: SupplierProfile{
			PartyID: r.ID, PaymentTermsDays: r.PaymentTermsDays, SuppliedGoodsNote: r.SuppliedGoodsNote,
			Status: r.Status, CreatedAt: r.ProfileCreatedAt, UpdatedAt: r.ProfileUpdatedAt, Version: r.ProfileVersion,
		},
		IsCustomer: r.IsCustomer,
	}
}

var supplierColumns = prefixed("p", partyColumns) + `,
	       s.payment_terms_days, s.supplied_goods_note, s.status,
	       s.created_at AS profile_created_at, s.updated_at AS profile_updated_at, s.version AS profile_version,
	       EXISTS (SELECT 1 FROM customer_profiles c WHERE c.party_id = p.id) AS is_customer`

const supplierFrom = `
	FROM parties p
	JOIN supplier_profiles s ON s.party_id = p.id`

var supplierSelect = `SELECT ` + supplierColumns + supplierFrom

// supplierListSelect adds the SupplierActivity columns to supplierSelect.
var supplierListSelect = `SELECT ` + supplierColumns + `,
	       (SELECT c.name FROM contacts c WHERE c.party_id = p.id ORDER BY c.is_primary DESC, c.created_at, c.id LIMIT 1) AS contact_name,
	       (SELECT count(*) FROM purchase_orders po WHERE po.supplier_party_id = p.id) AS order_count,
	       (SELECT max(r.business_date) FROM receipts r
	        WHERE r.supplier_party_id = p.id AND r.reversal_of IS NULL AND r.status <> 'reversed') AS last_receipt_date,
	       COALESCE((SELECT sum(o.amount - o.settled_amount) FROM obligations o
	        WHERE o.party_id = p.id AND o.direction = 'payable' AND o.status = 'open'), 0) AS payable_open,
	       ` + fmt.Sprintf(supplierOpenOrders, "p.id") + ` AS has_open_orders,
	       (` + fmt.Sprintf(supplierUsage, "p.id") + `) AS in_use` + supplierFrom

// supplierListRow is one row of supplierListSelect.
type supplierListRow struct {
	supplierRow
	SupplierActivity
}

func readSupplier(ctx context.Context, q db.Querier, id uuid.UUID) (Supplier, error) {
	var r supplierRow
	if err := q.QueryRowxContext(ctx, supplierSelect+` WHERE p.id = $1`, id).StructScan(&r); err != nil {
		if db.IsNoRows(err) {
			return Supplier{}, errNotFound()
		}
		return Supplier{}, fmt.Errorf("partner: read supplier: %w", err)
	}
	return r.supplier(), nil
}

// SupplierFilter selects suppliers; AfterKey/AfterID continue after a (name_key, id) cursor.
type SupplierFilter struct {
	Q        string
	Status   string
	AfterKey *string
	AfterID  uuid.UUID
	Limit    int
}

func listSuppliers(ctx context.Context, q db.Querier, f SupplierFilter, patterns []string) ([]Supplier, error) {
	afterKey := ""
	if f.AfterKey != nil {
		afterKey = *f.AfterKey
	}
	var rows []supplierListRow
	// The search also finds the suppliers of an item: a supplier whose confirmed purchase orders
	// carry a SKU matching every term (Nhập hàng: "who sold me this?").
	if err := sqlx.SelectContext(ctx, q, &rows, supplierListSelect+`
		WHERE ($1 = '' OR s.status = $1)
		  AND (cardinality($2::text[]) = 0 OR p.search_norm LIKE ALL ($2::text[])
		    OR EXISTS (SELECT 1 FROM purchase_orders po
		      JOIN purchase_order_lines l ON l.purchase_order_id = po.id JOIN skus k ON k.id = l.sku_id
		      WHERE po.supplier_party_id = p.id AND po.status <> 'draft' AND k.search_norm LIKE ALL ($2::text[])))
		  AND (NOT $3 OR (p.name_key, p.id) > ($4, $5::uuid))
		ORDER BY p.name_key, p.id
		LIMIT $6`, f.Status, patterns, f.AfterKey != nil, afterKey, f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("partner: list suppliers: %w", err)
	}
	out := make([]Supplier, len(rows))
	for i, r := range rows {
		out[i] = r.supplier()
		out[i].Activity = &rows[i].SupplierActivity
	}
	return out, nil
}

func readGroup(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (CustomerGroup, error) {
	query := `SELECT ` + groupColumns + ` FROM customer_groups WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var g CustomerGroup
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&g); err != nil {
		if db.IsNoRows(err) {
			return CustomerGroup{}, errNotFound()
		}
		return CustomerGroup{}, fmt.Errorf("partner: read customer group: %w", err)
	}
	return g, nil
}

// defaultGroupID is the built-in retail group new customers join when no group is given.
func defaultGroupID(ctx context.Context, q db.Querier) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `
		SELECT id FROM customer_groups
		WHERE is_system AND active AND base_price_kind = 'retail'
		ORDER BY created_at, id LIMIT 1`).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, errInvalid(map[string]string{"customer_group_id": "required"})
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("partner: read default customer group: %w", err)
	}
	return id, nil
}

func readContact(ctx context.Context, q db.Querier, partyID, id uuid.UUID) (Contact, error) {
	var c Contact
	err := q.QueryRowxContext(ctx, `SELECT `+contactColumns+` FROM contacts WHERE id = $1 AND party_id = $2 FOR UPDATE`,
		id, partyID).StructScan(&c)
	if db.IsNoRows(err) {
		return Contact{}, errNotFound()
	}
	if err != nil {
		return Contact{}, fmt.Errorf("partner: read contact: %w", err)
	}
	return c, nil
}

func readAddress(ctx context.Context, q db.Querier, partyID, id uuid.UUID) (Address, error) {
	var a Address
	err := q.QueryRowxContext(ctx, `SELECT `+addressColumns+` FROM party_addresses WHERE id = $1 AND party_id = $2 FOR UPDATE`,
		id, partyID).StructScan(&a)
	if db.IsNoRows(err) {
		return Address{}, errNotFound()
	}
	if err != nil {
		return Address{}, fmt.Errorf("partner: read address: %w", err)
	}
	return a, nil
}

// deletePartyIfOrphan removes a party with its contacts and addresses once it has neither a
// customer nor a supplier profile.
func deletePartyIfOrphan(ctx context.Context, q db.Querier, partyID uuid.UUID) error {
	var hasProfile bool
	if err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM supplier_profiles WHERE party_id = $1)`, partyID).Scan(&hasProfile); err != nil {
		return fmt.Errorf("partner: check party profiles: %w", err)
	}
	if hasProfile {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM contacts WHERE party_id = $1`,
		`DELETE FROM party_addresses WHERE party_id = $1`,
		`DELETE FROM parties WHERE id = $1`,
	} {
		if _, err := q.ExecContext(ctx, stmt, partyID); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errPartyInUse()
			}
			return fmt.Errorf("partner: delete party: %w", err)
		}
	}
	return nil
}

// SQLUsageReader implements UsageReader with read-only queries on the tables of the modules that
// reference partners. It is replaced by their own readers as they are built.
type SQLUsageReader struct{}

// supplierUsage is true when a purchase document, a payable or an outgoing payment references
// the party %[1]s as a supplier.
const supplierUsage = `
		EXISTS (SELECT 1 FROM purchase_orders WHERE supplier_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM receipts WHERE supplier_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM supplier_documents WHERE supplier_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM purchase_returns WHERE supplier_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM purchase_price_adjustments WHERE supplier_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM landed_cost_allocations WHERE payee_party_id = %[1]s)
		    OR EXISTS (SELECT 1 FROM obligations WHERE party_id = %[1]s AND direction = 'payable')
		    OR EXISTS (SELECT 1 FROM credit_notes WHERE party_id = %[1]s AND direction = 'payable')
		    OR EXISTS (SELECT 1 FROM debt_adjustments WHERE party_id = %[1]s AND direction = 'payable')
		    OR EXISTS (SELECT 1 FROM statements WHERE party_id = %[1]s AND direction = 'payable')
		    OR EXISTS (SELECT 1 FROM collection_notes WHERE party_id = %[1]s AND direction = 'payable')
		    OR EXISTS (SELECT 1 FROM payments WHERE party_id = %[1]s AND direction = 'out')
		    OR EXISTS (SELECT 1 FROM debt_offsets WHERE party_id = %[1]s)`

// supplierOpenOrders is true while a confirmed purchase order of the party %[1]s still waits for
// goods.
const supplierOpenOrders = `EXISTS (SELECT 1 FROM purchase_orders
		WHERE supplier_party_id = %[1]s AND status IN ('confirmed', 'partially_received'))`

// SupplierInUse implements UsageReader.
func (SQLUsageReader) SupplierInUse(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error) {
	return exists(ctx, q, "supplier usage", `SELECT `+fmt.Sprintf(supplierUsage, "$1"), partyID)
}

// SupplierHasOpenOrders implements UsageReader.
func (SQLUsageReader) SupplierHasOpenOrders(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error) {
	return exists(ctx, q, "supplier open orders", `SELECT `+fmt.Sprintf(supplierOpenOrders, "$1"), partyID)
}

// CustomerInUse implements UsageReader.
func (SQLUsageReader) CustomerInUse(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error) {
	return exists(ctx, q, "customer usage", `
		SELECT EXISTS (SELECT 1 FROM quotes WHERE customer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM invoices WHERE customer_party_id = $1 OR payer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM sales_documents WHERE customer_party_id = $1 OR payer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM pos_carts WHERE customer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM sales_returns WHERE customer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM exchanges WHERE customer_party_id = $1)
		    OR EXISTS (SELECT 1 FROM price_lists WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM obligations WHERE party_id = $1 AND direction = 'receivable')
		    OR EXISTS (SELECT 1 FROM credit_notes WHERE party_id = $1 AND direction = 'receivable')
		    OR EXISTS (SELECT 1 FROM debt_adjustments WHERE party_id = $1 AND direction = 'receivable')
		    OR EXISTS (SELECT 1 FROM statements WHERE party_id = $1 AND direction = 'receivable')
		    OR EXISTS (SELECT 1 FROM collection_notes WHERE party_id = $1 AND direction = 'receivable')
		    OR EXISTS (SELECT 1 FROM payments WHERE party_id = $1 AND direction = 'in')
		    OR EXISTS (SELECT 1 FROM debt_offsets WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM customer_activities WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM customer_care_tasks WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM consents WHERE party_id = $1)
		    OR EXISTS (SELECT 1 FROM loyalty_entries WHERE party_id = $1)`, partyID)
}

// CustomerGroupInUse implements UsageReader.
func (SQLUsageReader) CustomerGroupInUse(ctx context.Context, q db.Querier, groupID uuid.UUID) (bool, error) {
	return exists(ctx, q, "customer group usage", `
		SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE customer_group_id = $1)
		    OR EXISTS (SELECT 1 FROM price_lists WHERE customer_group_id = $1)
		    OR EXISTS (SELECT 1 FROM promotions WHERE customer_group_id = $1)`, groupID)
}

func exists(ctx context.Context, q db.Querier, what, query string, args ...any) (bool, error) {
	var found bool
	if err := q.QueryRowxContext(ctx, query, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("partner: %s: %w", what, err)
	}
	return found, nil
}
