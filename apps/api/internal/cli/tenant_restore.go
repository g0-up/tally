package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jmoiron/sqlx"
	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
)

const (
	// restoreStatementTimeout bounds one statement of the restore tooling: whole-table counts,
	// copies and deletes of a large store run far longer than a request.
	restoreStatementTimeout = 10 * time.Minute
	// copyBatchRows is how many rows one INSERT of a tenant copy carries.
	copyBatchRows = 500
)

// newTenantRestoreCommands are the per-tenant restore tools: check a restored backup, copy one
// tenant out of it, hold the tenant in maintenance meanwhile, and purge a closed tenant.
func newTenantRestoreCommands(lookup config.Lookup) []*cobra.Command {
	return []*cobra.Command{
		newRestoreCheckCommand(),
		newTenantMaintenanceCommand(lookup),
		newTenantCopyCommand(lookup),
		newTenantPurgeCommand(lookup),
	}
}

func openRestoreDB(ctx context.Context, url string) (*db.DB, error) {
	return db.Open(ctx, url, db.Options{MaxConns: 2, StatementTimeout: restoreStatementTimeout, ApplicationName: "tally-cli"})
}

func newRestoreCheckCommand() *cobra.Command {
	var tenant, scratchURL string
	cmd := &cobra.Command{
		Use:   "restore-check",
		Short: "Count one tenant's rows in a restored backup and verify its ledgers there",
		Long: "Run against a scratch database restored from a backup (see docs/operations/tenant-restore.md).\n" +
			"Prints the tenant's row count per table and runs the ledger verification on the scratch copy;\n" +
			"a non-zero exit means the backup is not a sound source for a restore.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := ids.Parse(tenant)
			if err != nil {
				return fmt.Errorf("--tenant must be a tenant id: %w", err)
			}
			scratch, err := openRestoreDB(cmd.Context(), scratchURL)
			if err != nil {
				return fmt.Errorf("scratch database: %w", err)
			}
			defer func() { _ = scratch.Close() }()
			return restoreCheck(cmd.Context(), scratch, id, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	cmd.Flags().StringVar(&scratchURL, "scratch-url", "", "connection string of the scratch database (role tally_app)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("scratch-url")
	return cmd
}

// restoreCheck reports the tenant's rows in the scratch database and verifies its ledgers there.
func restoreCheck(ctx context.Context, scratch *db.DB, tenantID uuid.UUID, w io.Writer) error {
	t, err := readRestoreTenant(ctx, scratch, tenantID)
	if err != nil {
		return fmt.Errorf("scratch database: %w", err)
	}
	if _, err := fmt.Fprintf(w, "scratch tenant %s (%s) status=%s\n", t.Slug, tenantID, t.Status); err != nil {
		return err
	}
	var counts []tableCount
	err = scratch.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		tables, err := db.TenantTables(ctx, q)
		if err != nil {
			return err
		}
		counts, err = countTenantRows(ctx, q, tenantID, tables)
		return err
	})
	if err != nil {
		return fmt.Errorf("count scratch rows: %w", err)
	}
	if err := printCounts(w, counts); err != nil {
		return err
	}
	return verifyLedgers(ctx, scratch, tenantID.String(), ledgerVerifiers, w)
}

type restoreTenant struct {
	Slug        string `db:"slug"`
	Status      string `db:"status"`
	IsSandbox   bool   `db:"is_sandbox"`
	LegalHold   bool   `db:"legal_hold"`
	Maintenance bool   `db:"maintenance"`
}

func readRestoreTenant(ctx context.Context, d *db.DB, tenantID uuid.UUID) (restoreTenant, error) {
	var t restoreTenant
	err := d.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.GetContext(ctx, q, &t, `SELECT slug, status, is_sandbox, legal_hold, maintenance FROM tenants WHERE id = $1`, tenantID)
	})
	if db.IsNoRows(err) {
		return restoreTenant{}, fmt.Errorf("no tenant has the id %s", tenantID)
	}
	if err != nil {
		return restoreTenant{}, fmt.Errorf("read tenant %s: %w", tenantID, err)
	}
	return t, nil
}

type tableCount struct {
	table string
	rows  int64
}

func countTenantRows(ctx context.Context, q db.Querier, tenantID uuid.UUID, tables []db.TenantTable) ([]tableCount, error) {
	out := make([]tableCount, 0, len(tables))
	for _, t := range tables {
		var n int64
		query := `SELECT count(*) FROM ` + pgx.Identifier{"tally", t.Name}.Sanitize() + ` WHERE tenant_id = $1`
		if err := q.QueryRowxContext(ctx, query, tenantID).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", t.Name, err)
		}
		out = append(out, tableCount{table: t.Name, rows: n})
	}
	return out, nil
}

func printCounts(w io.Writer, counts []tableCount) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var total int64
	for _, c := range counts {
		if c.rows == 0 {
			continue
		}
		total += c.rows
		if _, err := fmt.Fprintf(tw, "  %s\t%d\n", c.table, c.rows); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%d rows in %d tenant tables\n", total, len(counts))
	return err
}

func newTenantMaintenanceCommand(lookup config.Lookup) *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:       "maintenance on|off",
		Short:     "Hold a tenant in maintenance (every posting refused) or release it",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := ids.Parse(tenant)
			if err != nil {
				return fmt.Errorf("--tenant must be a tenant id: %w", err)
			}
			cfg, err := config.LoadCLI(lookup)
			if err != nil {
				return err
			}
			database, err := openRestoreDB(cmd.Context(), cfg.Database.URL)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			on := args[0] == "on"
			if err := setMaintenance(cmd.Context(), database, id, on); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "tenant %s maintenance=%t\n", id, on)
			return err
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}

func setMaintenance(ctx context.Context, d *db.DB, tenantID uuid.UUID, on bool) error {
	return d.WithPlatformTx(ctx, func(q db.Querier) error {
		res, err := q.ExecContext(ctx, `UPDATE tenants SET maintenance = $2 WHERE id = $1`, tenantID, on)
		if err != nil {
			return fmt.Errorf("set maintenance: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(fmt.Errorf("no tenant has the id %s", tenantID), err)
		}
		return nil
	})
}

func newTenantCopyCommand(lookup config.Lookup) *cobra.Command {
	var tenant, from string
	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy one tenant's missing rows from a restored backup into the live database",
		Long: "Reads the tenant's rows from the scratch database and inserts those the live database lacks,\n" +
			"as tally_app under row security for that tenant only; rows already present are kept. The tenant\n" +
			"must be in maintenance. The copy commits only when the primary key of every scratch row is in the\n" +
			"live table and the ledgers verify; otherwise nothing is written and the missing keys are listed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := ids.Parse(tenant)
			if err != nil {
				return fmt.Errorf("--tenant must be a tenant id: %w", err)
			}
			cfg, err := config.LoadCLI(lookup)
			if err != nil {
				return err
			}
			live, err := openRestoreDB(cmd.Context(), cfg.Database.URL)
			if err != nil {
				return err
			}
			defer func() { _ = live.Close() }()
			scratch, err := openRestoreDB(cmd.Context(), from)
			if err != nil {
				return fmt.Errorf("scratch database: %w", err)
			}
			defer func() { _ = scratch.Close() }()
			return copyTenant(cmd.Context(), live, scratch, id, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	cmd.Flags().StringVar(&from, "from", "", "connection string of the scratch database (role tally_app)")
	_ = cmd.MarkFlagRequired("tenant")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}

type copyResult struct {
	table                            string
	scratch, before, inserted, after int64
	// missing counts the scratch rows whose primary key is not live after the copy: another
	// unique key of a live row refused them. missingKeys holds the first few.
	missing     int64
	missingKeys []string
}

// missingKeysShown caps the keys of missing rows a copy prints per table.
const missingKeysShown = 5

// copyTenant inserts the tenant's scratch rows the live database lacks. Every read and write is
// a tenant transaction for tenantID, so row security confines both sides to that tenant.
func copyTenant(ctx context.Context, live, scratch *db.DB, tenantID uuid.UUID, w io.Writer) error {
	t, err := readRestoreTenant(ctx, live, tenantID)
	if err != nil {
		return err
	}
	if !t.Maintenance {
		return fmt.Errorf("tenant %s is not in maintenance; run `tally tenant maintenance on --tenant %s` first", tenantID, tenantID)
	}
	if _, err := readRestoreTenant(ctx, scratch, tenantID); err != nil {
		return fmt.Errorf("scratch database: %w", err)
	}
	tables, selfRefs, err := matchingSchemas(ctx, live, scratch)
	if err != nil {
		return err
	}
	keys, err := primaryKeys(ctx, live, tables)
	if err != nil {
		return err
	}
	var results []copyResult
	var mismatches []string
	copyErr := scratch.WithReadOnlyTenantTx(ctx, tenantID, func(sq db.Querier) error {
		return live.WithTenantTx(ctx, tenantID, func(lq db.Querier) error {
			inserted := map[string][]uuid.UUID{}
			for _, table := range tables {
				r, ids, err := copyTable(ctx, sq, lq, tenantID, table, keys[table.Name], slices.Contains(selfRefs, table.Name))
				if err != nil {
					return err
				}
				results = append(results, r)
				inserted[table.Name] = ids
			}
			for _, table := range tables {
				if err := fillDeferred(ctx, sq, lq, tenantID, table, inserted[table.Name]); err != nil {
					return err
				}
			}
			var short []string
			for _, r := range results {
				if r.missing > 0 {
					short = append(short, r.table)
				}
			}
			for _, v := range ledgerVerifiers {
				lines, err := v.verify(ctx, lq)
				if err != nil {
					return err
				}
				for _, line := range lines {
					mismatches = append(mismatches, v.name+": "+line)
				}
			}
			if len(short) > 0 || len(mismatches) > 0 {
				return fmt.Errorf("%d tables lack scratch rows a live row's unique key refused (%s) and %d projections disagree with their ledgers",
					len(short), strings.Join(short, ", "), len(mismatches))
			}
			return nil
		})
	})
	if err := printCopy(w, results, mismatches); err != nil {
		return errors.Join(copyErr, err)
	}
	if copyErr != nil {
		return fmt.Errorf("copy rolled back, nothing written: %w", copyErr)
	}
	var inserted int64
	for _, r := range results {
		inserted += r.inserted
	}
	_, err = fmt.Fprintf(w, "copied %d rows into tenant %s (%s); ledgers verified\n", inserted, t.Slug, tenantID)
	return err
}

// matchingSchemas lists the tenant tables, parents first, and the tables that reference
// themselves. It refuses when the scratch database does not have the live tables and columns.
func matchingSchemas(ctx context.Context, live, scratch *db.DB) ([]db.TenantTable, []string, error) {
	var liveTables, scratchTables []db.TenantTable
	var selfRefs []string
	err := live.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		if liveTables, err = db.TenantTables(ctx, q); err != nil {
			return err
		}
		return sqlx.SelectContext(ctx, q, &selfRefs, `
			SELECT DISTINCT c.relname FROM pg_constraint k
			JOIN pg_class c ON c.oid = k.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE k.contype = 'f' AND k.conrelid = k.confrelid AND n.nspname = 'tally'`)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read live schema: %w", err)
	}
	if err := scratch.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		scratchTables, err = db.TenantTables(ctx, q)
		return err
	}); err != nil {
		return nil, nil, fmt.Errorf("read scratch schema: %w", err)
	}
	columns := make(map[string][]string, len(scratchTables))
	for _, t := range scratchTables {
		columns[t.Name] = t.Columns
	}
	for _, t := range liveTables {
		if c, ok := columns[t.Name]; !ok || !slices.Equal(c, t.Columns) {
			return nil, nil, fmt.Errorf("table %s differs between the live and scratch databases; restore a backup taken at the live schema version", t.Name)
		}
	}
	if len(columns) != len(liveTables) {
		return nil, nil, errors.New("the scratch database has tenant tables the live database lacks; restore a backup taken at the live schema version")
	}
	return liveTables, selfRefs, nil
}

// primaryKeys returns the primary key columns of every tenant table. A copy proves each scratch
// row arrived by its key, so a table without one is refused.
func primaryKeys(ctx context.Context, live *db.DB, tables []db.TenantTable) (map[string][]string, error) {
	var rows []struct {
		Table   string              `db:"table_name"`
		Columns db.JSONList[string] `db:"columns"`
	}
	if err := live.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &rows, `
			SELECT c.relname AS table_name,
			       to_jsonb(array_agg(a.attname::text ORDER BY array_position(k.conkey, a.attnum))) AS columns
			FROM pg_constraint k
			JOIN pg_class c ON c.oid = k.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(k.conkey)
			WHERE k.contype = 'p' AND n.nspname = 'tally'
			GROUP BY c.relname`)
	}); err != nil {
		return nil, fmt.Errorf("read primary keys: %w", err)
	}
	keys := make(map[string][]string, len(rows))
	for _, r := range rows {
		keys[r.Table] = r.Columns
	}
	for _, t := range tables {
		if len(keys[t.Name]) == 0 {
			return nil, fmt.Errorf("table %s has no primary key; a copy cannot prove its rows arrived", t.Name)
		}
	}
	return keys, nil
}

// missingQuery lists the rows of a JSON batch whose primary key is not in the live table, with
// how many there are in all.
func missingQuery(table db.TenantTable, key []string) string {
	ident := pgx.Identifier{"tally", table.Name}.Sanitize()
	match := make([]string, len(key))
	text := make([]string, len(key))
	for i, c := range key {
		col := pgx.Identifier{c}.Sanitize()
		match[i] = "l." + col + " = s." + col
		text[i] = "s." + col + "::text"
	}
	return `SELECT count(*) OVER () AS total, concat_ws('/', ` + strings.Join(text, ", ") + `) AS key
		FROM json_populate_recordset(NULL::` + ident + `, $1::json) s
		WHERE NOT EXISTS (SELECT 1 FROM ` + ident + ` l WHERE ` + strings.Join(match, " AND ") + `)
		LIMIT ` + strconv.Itoa(missingKeysShown)
}

// copyTable streams the scratch rows of one table into the live table in batches; rows whose
// key is already live are skipped, and each batch then counts its rows whose key is still not
// live (another unique key refused them). A table that references itself goes in one statement,
// so a row never precedes the row it references across batches. Deferred reference columns are
// inserted empty; the ids of the inserted rows are returned for fillDeferred.
func copyTable(ctx context.Context, sq, lq db.Querier, tenantID uuid.UUID, table db.TenantTable, key []string, selfRef bool) (copyResult, []uuid.UUID, error) {
	r := copyResult{table: table.Name}
	ident := pgx.Identifier{"tally", table.Name}.Sanitize()
	count := `SELECT count(*) FROM ` + ident + ` WHERE tenant_id = $1`
	if err := lq.QueryRowxContext(ctx, count, tenantID).Scan(&r.before); err != nil {
		return r, nil, fmt.Errorf("count live %s: %w", table.Name, err)
	}
	insert, err := copyInsert(table)
	if err != nil {
		return r, nil, err
	}
	missing := missingQuery(table, key)
	var (
		batch    []string
		inserted []uuid.UUID
	)
	check := func(payload string) error {
		var lost []struct {
			Total int64  `db:"total"`
			Key   string `db:"key"`
		}
		if err := sqlx.SelectContext(ctx, lq, &lost, missing, payload); err != nil {
			return fmt.Errorf("check the copied keys of %s: %w", table.Name, err)
		}
		if len(lost) > 0 {
			r.missing += lost[0].Total
		}
		for _, l := range lost {
			if len(r.missingKeys) < missingKeysShown {
				r.missingKeys = append(r.missingKeys, l.Key)
			}
		}
		return nil
	}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		payload := "[" + strings.Join(batch, ",") + "]"
		batch = batch[:0]
		if len(table.Deferred) == 0 {
			res, err := lq.ExecContext(ctx, insert, payload)
			if err != nil {
				return fmt.Errorf("insert into %s: %w", table.Name, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("insert into %s: %w", table.Name, err)
			}
			r.inserted += n
			return check(payload)
		}
		var got []uuid.UUID
		if err := sqlx.SelectContext(ctx, lq, &got, insert, payload); err != nil {
			return fmt.Errorf("insert into %s: %w", table.Name, err)
		}
		r.inserted += int64(len(got))
		inserted = append(inserted, got...)
		return check(payload)
	}
	err = streamRows(ctx, sq, `SELECT row_to_json(t)::text FROM `+ident+` t WHERE tenant_id = $1`, tenantID, func(row string) error {
		r.scratch++
		batch = append(batch, row)
		if !selfRef && len(batch) >= copyBatchRows {
			return flush()
		}
		return nil
	})
	if err != nil {
		return r, nil, fmt.Errorf("copy %s: %w", table.Name, err)
	}
	if err := flush(); err != nil {
		return r, nil, err
	}
	if err := lq.QueryRowxContext(ctx, count, tenantID).Scan(&r.after); err != nil {
		return r, nil, fmt.Errorf("count live %s: %w", table.Name, err)
	}
	return r, inserted, nil
}

// copyInsert is the INSERT of a batch of JSON rows into table. With deferred references it
// leaves their columns empty and returns the ids of the inserted rows.
func copyInsert(table db.TenantTable) (string, error) {
	ident := pgx.Identifier{"tally", table.Name}.Sanitize()
	if len(table.Deferred) == 0 {
		return `INSERT INTO ` + ident + ` SELECT * FROM json_populate_recordset(NULL::` + ident + `, $1::json) ON CONFLICT DO NOTHING`, nil
	}
	if !slices.Contains(table.Columns, "id") {
		return "", fmt.Errorf("table %s has a deferred reference but no id column", table.Name)
	}
	empty := deferredColumns(table)
	cols := make([]string, len(table.Columns))
	values := make([]string, len(table.Columns))
	for i, c := range table.Columns {
		cols[i] = pgx.Identifier{c}.Sanitize()
		values[i] = "s." + cols[i]
		if slices.Contains(empty, c) {
			values[i] = "NULL"
		}
	}
	return `INSERT INTO ` + ident + ` (` + strings.Join(cols, ", ") + `) SELECT ` + strings.Join(values, ", ") +
		` FROM json_populate_recordset(NULL::` + ident + `, $1::json) s ON CONFLICT DO NOTHING RETURNING id`, nil
}

func deferredColumns(table db.TenantTable) []string {
	var out []string
	for _, d := range table.Deferred {
		out = append(out, d.Columns...)
	}
	return out
}

// fillDeferred writes the deferred reference columns of the rows copyTable inserted, now that
// the rows they reference are in.
func fillDeferred(ctx context.Context, sq, lq db.Querier, tenantID uuid.UUID, table db.TenantTable, inserted []uuid.UUID) error {
	if len(table.Deferred) == 0 || len(inserted) == 0 {
		return nil
	}
	ident := pgx.Identifier{"tally", table.Name}.Sanitize()
	cols := deferredColumns(table)
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = pgx.Identifier{c}.Sanitize() + " = s." + pgx.Identifier{c}.Sanitize()
	}
	update := `UPDATE ` + ident + ` AS d SET ` + strings.Join(sets, ", ") +
		` FROM json_populate_recordset(NULL::` + ident + `, $1::json) s WHERE d.tenant_id = s.tenant_id AND d.id = s.id AND d.id = ANY($2)`
	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := lq.ExecContext(ctx, update, "["+strings.Join(batch, ",")+"]", inserted); err != nil {
			return fmt.Errorf("fill deferred references of %s: %w", table.Name, err)
		}
		batch = batch[:0]
		return nil
	}
	err := streamRows(ctx, sq, `SELECT row_to_json(t)::text FROM `+ident+` t WHERE tenant_id = $1`, tenantID, func(row string) error {
		batch = append(batch, row)
		if len(batch) >= copyBatchRows {
			return flush()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fill deferred references of %s: %w", table.Name, err)
	}
	return flush()
}

// streamRows runs query for tenantID and hands each text row to fn.
func streamRows(ctx context.Context, q db.Querier, query string, tenantID uuid.UUID, fn func(row string) error) error {
	rows, err := q.QueryxContext(ctx, query, tenantID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := fn(row); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

func printCopy(w io.Writer, results []copyResult, mismatches []string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "table\tscratch\tlive before\tinserted\tlive after\t"); err != nil {
		return err
	}
	for _, r := range results {
		if r.scratch == 0 && r.before == 0 {
			continue
		}
		mark := ""
		if r.missing > 0 {
			mark = fmt.Sprintf("MISSING %d", r.missing)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n", r.table, r.scratch, r.before, r.inserted, r.after, mark); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, r := range results {
		for _, k := range r.missingKeys {
			if _, err := fmt.Fprintf(w, "missing %s key %s\n", r.table, k); err != nil {
				return err
			}
		}
		if more := r.missing - int64(len(r.missingKeys)); more > 0 {
			if _, err := fmt.Fprintf(w, "missing %s and %d more rows\n", r.table, more); err != nil {
				return err
			}
		}
	}
	for _, m := range mismatches {
		if _, err := fmt.Fprintf(w, "ledger mismatch %s\n", m); err != nil {
			return err
		}
	}
	return nil
}

func newTenantPurgeCommand(lookup config.Lookup) *cobra.Command {
	var (
		tenant, backupFile string
		opts               purgeOptions
	)
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete every row of a closed tenant and its sandboxes, and retire its store address",
		Long: "Runs with the migration role (MIGRATE_DATABASE_URL). Only a store closed at its owner's request\n" +
			"(its latest closure request completed) and without legal hold on it or any sandbox can be\n" +
			"purged; --confirm-slug must repeat its store address, and outside development and test a fresh\n" +
			"--backup-file is required. --dry-run prints the rows the purge would delete and keeps them. The\n" +
			"store address is retired in the same transaction and never issued again. Files in the object\n" +
			"store are not deleted; the command prints their key prefixes. Restart the API afterwards so its\n" +
			"host cache forgets the address.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := ids.Parse(tenant)
			if err != nil {
				return fmt.Errorf("--tenant must be a tenant id: %w", err)
			}
			if !opts.dryRun && opts.confirmSlug == "" {
				return errors.New("--confirm-slug must repeat the store address of the tenant to purge")
			}
			base, err := config.LoadBase(lookup)
			if err != nil {
				return err
			}
			if !opts.dryRun && backupRequired(lookup, base) {
				if err := checkBackup(backupFile, backupClock()); err != nil {
					return err
				}
			}
			cfg, err := config.LoadMigrate(lookup)
			if err != nil {
				return err
			}
			owner, err := openRestoreDB(cmd.Context(), cfg.URL)
			if err != nil {
				return err
			}
			defer func() { _ = owner.Close() }()
			return purgeTenant(cmd.Context(), owner, id, opts, clock.System{}, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id")
	cmd.Flags().StringVar(&opts.confirmSlug, "confirm-slug", "", "the store address of the tenant, repeated (required unless --dry-run)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print the rows the purge would delete and delete nothing")
	addBackupFlag(cmd, &backupFile)
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}

// purgeOptions are the operator's flags of a purge.
type purgeOptions struct {
	// confirmSlug must equal the tenant's store address when set; a real purge requires it.
	confirmSlug string
	dryRun      bool
}

// errPurgeDryRun rolls back the transaction of a dry run.
var errPurgeDryRun = errors.New("dry run")

// purgeSandbox is a sandbox of the store being purged.
type purgeSandbox struct {
	ID        uuid.UUID `db:"id"`
	LegalHold bool      `db:"legal_hold"`
}

// purgeTenant deletes the rows of a store closed at its owner's request and of its sandboxes,
// children before parents, then the tenant rows, and retires the store address, all in one
// transaction; a dry run rolls it back after counting. owner connects as the schema owner, which
// row security still confines to the tenant set in app.tenant_id. Object-store files are kept.
func purgeTenant(ctx context.Context, owner *db.DB, tenantID uuid.UUID, opts purgeOptions, clk clock.Clock, w io.Writer) error {
	var (
		slug      string
		sandboxes []purgeSandbox
		deleted   = map[string]int64{}
	)
	err := owner.WithPlatformTx(ctx, func(q db.Querier) error {
		var t restoreTenant
		err := sqlx.GetContext(ctx, q, &t, `SELECT slug, status, is_sandbox, legal_hold, maintenance FROM tenants WHERE id = $1 FOR UPDATE`, tenantID)
		if db.IsNoRows(err) {
			return fmt.Errorf("no tenant has the id %s", tenantID)
		}
		if err != nil {
			return fmt.Errorf("read tenant: %w", err)
		}
		switch {
		case t.IsSandbox:
			return fmt.Errorf("tenant %s is a sandbox; purge the store it belongs to", tenantID)
		case t.Status != "closed":
			return fmt.Errorf("tenant %s is %s; only a closed store can be purged", tenantID, t.Status)
		case t.LegalHold:
			return fmt.Errorf("tenant %s is under legal hold; release the hold before purging", tenantID)
		case opts.confirmSlug != "" && opts.confirmSlug != t.Slug:
			return fmt.Errorf("--confirm-slug %q does not match the store address %q of tenant %s", opts.confirmSlug, t.Slug, tenantID)
		}
		var closure string
		err = sqlx.GetContext(ctx, q, &closure, `SELECT status FROM account_closure_requests WHERE tenant_id = $1
			ORDER BY created_at DESC, id DESC LIMIT 1`, tenantID)
		if err != nil && !db.IsNoRows(err) {
			return fmt.Errorf("read closure request: %w", err)
		}
		if closure != "completed" {
			return fmt.Errorf("tenant %s is closed but its closure request did not complete (a lapsed subscription, not "+
				"the owner's request); only a store closed at its owner's request can be purged", tenantID)
		}
		slug = t.Slug
		if err := sqlx.SelectContext(ctx, q, &sandboxes, `SELECT id, legal_hold FROM tenants WHERE sandbox_of = $1
			ORDER BY created_at, id FOR UPDATE`, tenantID); err != nil {
			return fmt.Errorf("list sandboxes: %w", err)
		}
		for _, sb := range sandboxes {
			if sb.LegalHold {
				return fmt.Errorf("sandbox %s is under legal hold; release the hold before purging", sb.ID)
			}
		}
		tables, err := db.TenantOwnedTables(ctx, q)
		if err != nil {
			return err
		}
		for _, id := range append(sandboxIDs(sandboxes), tenantID) {
			if err := purgeRows(ctx, q, id, tables, deleted); err != nil {
				return fmt.Errorf("purge tenant %s: %w", id, err)
			}
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM tenants WHERE sandbox_of = $1`, tenantID); err != nil {
			return fmt.Errorf("delete sandboxes: %w", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
			return fmt.Errorf("delete tenant: %w", err)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO retired_tenant_slugs (slug, tenant_id, retired_at) VALUES ($1, $2, $3)`,
			slug, tenantID, clk.Now()); err != nil {
			return fmt.Errorf("retire store address: %w", err)
		}
		if opts.dryRun {
			return errPurgeDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errPurgeDryRun) {
		return err
	}
	counts := make([]tableCount, 0, len(deleted))
	for table, n := range deleted {
		counts = append(counts, tableCount{table: table, rows: n})
	}
	slices.SortFunc(counts, func(a, b tableCount) int { return strings.Compare(a.table, b.table) })
	if err := printCounts(w, counts); err != nil {
		return err
	}
	if opts.dryRun {
		_, err = fmt.Fprintf(w, "dry run: nothing deleted; purging tenant %s (%s) would delete these rows of it and its %d sandboxes\n",
			slug, tenantID, len(sandboxes))
		return err
	}
	if _, err := fmt.Fprintf(w, "purged tenant %s (%s) and %d sandboxes; address %q retired; restart the API to evict it from the host cache\n",
		slug, tenantID, len(sandboxes), slug); err != nil {
		return err
	}
	for _, id := range append([]uuid.UUID{tenantID}, sandboxIDs(sandboxes)...) {
		if _, err := fmt.Fprintf(w, "object store files kept under %s\n", objectstore.TenantKey(id)); err != nil {
			return err
		}
	}
	return nil
}

func sandboxIDs(sandboxes []purgeSandbox) []uuid.UUID {
	out := make([]uuid.UUID, len(sandboxes))
	for i, sb := range sandboxes {
		out[i] = sb.ID
	}
	return out
}

// ownedRows is the condition selecting the rows of tenant $1 in t.
func ownedRows(t db.TenantOwnedTable) string {
	if t.Via == nil {
		return `tenant_id = $1`
	}
	return pgx.Identifier{t.Via.Column}.Sanitize() + ` IN (SELECT ` + pgx.Identifier{t.Via.ParentColumn}.Sanitize() +
		` FROM ` + pgx.Identifier{"tally", t.Via.Parent}.Sanitize() + ` WHERE tenant_id = $1)`
}

// purgeRows deletes the rows of one tenant from every tenant-owned table, children first, after
// clearing the references that close a cycle.
func purgeRows(ctx context.Context, q db.Querier, tenantID uuid.UUID, tables []db.TenantOwnedTable, deleted map[string]int64) error {
	if _, err := q.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	for _, t := range tables {
		if len(t.Deferred) == 0 {
			continue
		}
		var sets []string
		for _, d := range t.Deferred {
			for _, c := range d.Columns {
				sets = append(sets, pgx.Identifier{c}.Sanitize()+" = NULL")
			}
		}
		query := `UPDATE ` + pgx.Identifier{"tally", t.Name}.Sanitize() + ` SET ` + strings.Join(sets, ", ") + ` WHERE ` + ownedRows(t)
		if _, err := q.ExecContext(ctx, query, tenantID); err != nil {
			return fmt.Errorf("clear deferred references of %s: %w", t.Name, err)
		}
	}
	for i := len(tables) - 1; i >= 0; i-- {
		t := tables[i]
		res, err := q.ExecContext(ctx, `DELETE FROM `+pgx.Identifier{"tally", t.Name}.Sanitize()+` WHERE `+ownedRows(t), tenantID)
		if err != nil {
			return fmt.Errorf("delete from %s: %w", t.Name, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete from %s: %w", t.Name, err)
		}
		if n > 0 {
			deleted[t.Name] += n
		}
	}
	return nil
}
