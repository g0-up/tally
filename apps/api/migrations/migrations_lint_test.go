package migrations_test

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/g0-up/tally/apps/api/migrations"
)

// The database stores data and enforces structure; every behaviour lives in Go. This lint keeps
// procedural code, function calls and generated values out of the migrations. It works on SQL
// with comments removed and string literal contents blanked, so a forbidden word inside a comment
// or a CHECK literal never trips it and a forbidden construct cannot hide inside one.

// platformTables are outside RLS: authentication runs before a tenant is known. Every other table
// with a tenant_id column is a tenant table and must carry the tenant_isolation policy.
var platformTables = []string{
	"tenants", "retired_tenant_slugs", "users", "sessions", "auth_tokens", "mfa_totp_secrets",
	"mfa_recovery_codes", "login_attempts", "platform_admins", "platform_admin_sessions",
	"plans", "subscriptions", "entitlements", "saas_invoices", "saas_payment_events", "support_tickets",
	"support_ticket_messages", "support_sessions", "feature_flags", "feature_flag_changes",
	"tenant_usage_daily", "account_closure_requests", "product_feedback", "incidents", "partners",
	"tenant_referrals",
}

// appendOnlyTables deny UPDATE and DELETE to tally_app.
var appendOnlyTables = []string{
	"stock_moves", "price_changes", "audit_events", "period_locks", "cash_entries",
	"exception_confirmations", "invoice_versions", "statement_versions", "lot_genealogy",
	"loyalty_entries", "obligation_settlements", "customer_activities", "cogs_adjustments", "consents",
	"feature_flag_changes", "exception_confirmation_uses",
}

// maxIdentifier is PostgreSQL's NAMEDATALEN - 1; a longer name is silently truncated, so the
// name Go matches on (db.IsUniqueViolation and friends) would differ from the stored one.
const maxIdentifier = 63

// unledTenantIndexes are the secondary indexes of tenant tables allowed not to lead with
// tenant_id, with the reason (checked here for CREATE INDEX and against the catalog for every
// index). Primary keys on the global UUIDv7 id are exempt by design.
var unledTenantIndexes = map[string]string{
	"ix_products__attributes":     "GIN can only carry the uuid tenant_id through the btree_gin extension",
	"ux_attachments__storage_key": "object keys are unique across the shared bucket, not per tenant",
}

var (
	createIndexRe = regexp.MustCompile(`(?i)^CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(?:ONLY\s+)?(?:tally\.)?(\w+)\s*(?:USING\s+\w+\s*)?\(\s*(\w+)`)
	alterTableRe  = regexp.MustCompile(`(?i)^ALTER\s+TABLE\s+(?:ONLY\s+)?(?:tally\.)?([a-z_][a-z0-9_]*)\s+ADD\b`)
	constraintRe  = regexp.MustCompile(`(?i)\b(?:CONSTRAINT\s+([A-Za-z_][A-Za-z0-9_]*)\s+)?(PRIMARY\s+KEY|UNIQUE|CHECK|FOREIGN\s+KEY|REFERENCES)\b`)
	fkClauseEndRe = regexp.MustCompile(`(?i)\bFOREIGN\s+KEY\s*\([^()]*\)\s*$`)
	nameTailRe    = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
	fileNameRe    = regexp.MustCompile(`^(\d{6})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$`)
	searchPathRe  = regexp.MustCompile(`(?i)^SET\s+search_path\s*=\s*tally$`)
	createTableRe = regexp.MustCompile(`(?i)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:tally\.)?([a-z_][a-z0-9_]*)\s*\(`)
	tenantColRe   = regexp.MustCompile(`(?i)[(,]\s*tenant_id\s+uuid\b`)
	identParenRe  = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_$]*)\s*\(`)
	wordRe        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_$]*`)
)

// forbiddenPatterns apply to every statement, case-insensitively.
var forbiddenPatterns = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:CONSTRAINT\s+|EVENT\s+)?(FUNCTION|PROCEDURE|TRIGGER|RULE|AGGREGATE)\b`), "procedural object"},
	{regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+|RECURSIVE\s+|MATERIALIZED\s+)*VIEW\b`), "view"},
	{regexp.MustCompile(`(?i)\bCREATE\s+(EXTENSION|TYPE|DOMAIN|SEQUENCE|OPERATOR|CAST|LANGUAGE|CONVERSION)\b`), "forbidden object"},
	{regexp.MustCompile(`(?i)^DO\b`), "DO block"},
	{regexp.MustCompile(`(?i)\bLANGUAGE\b`), "LANGUAGE clause"},
	{regexp.MustCompile(`(?i)\bGENERATED\b`), "generated column or identity"},
	{regexp.MustCompile(`(?i)\bIDENTITY\b`), "identity column"},
	{regexp.MustCompile(`(?i)\b(?:SMALL|BIG)?SERIAL[248]?\b`), "serial column"},
	{regexp.MustCompile(`(?i)\bDEFAULT\s+(?:CURRENT_[A-Z_]+|LOCALTIME(?:STAMP)?|SESSION_USER|USER)\b`), "non-constant DEFAULT"},
	{regexp.MustCompile(`(?i)\b(?:CALL|EXECUTE|COPY)\b`), "procedural statement"},
}

// parenWords may be followed by "(" without being a function call: keywords, type modifiers and
// index methods.
var parenWords = map[string]bool{
	"CHECK": true, "KEY": true, "UNIQUE": true, "IN": true, "USING": true, "AND": true, "OR": true,
	"NOT": true, "DISTINCT": true, "VALUES": true, "WHERE": true, "NUMERIC": true, "DECIMAL": true,
	"CHAR": true, "CHARACTER": true, "VARCHAR": true, "VARYING": true, "GIN": true, "BTREE": true,
	"INCLUDE": true,
}

// tableNameWords precede a table name that is followed by its column list.
var tableNameWords = map[string]bool{"TABLE": true, "EXISTS": true, "REFERENCES": true, "INTO": true, "ON": true}

// policyFunctions are the built-ins sanctioned inside CREATE POLICY and nowhere else.
var policyFunctions = map[string]bool{"NULLIF": true, "CURRENT_SETTING": true}

// sanitize removes comments, blanks the contents of string literals (the quotes stay) and reports
// dollar quoting, which only exists to carry function or DO bodies.
func sanitize(sql string) (string, []string) {
	var out strings.Builder
	var problems []string
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			depth := 0
			for i < len(sql) {
				if strings.HasPrefix(sql[i:], "/*") {
					depth++
					i += 2
					continue
				}
				if strings.HasPrefix(sql[i:], "*/") {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
			if depth != 0 {
				problems = append(problems, "unterminated block comment")
			}
			out.WriteByte(' ')
		case c == '\'':
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				problems = append(problems, "unterminated string literal")
			}
			out.WriteString("''")
		case c == '$':
			j := i + 1
			for j < len(sql) && (sql[j] == '_' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || sql[j] >= '0' && sql[j] <= '9') {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				problems = append(problems, "dollar-quoted body "+sql[i:j+1])
			}
			out.WriteByte(c)
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), problems
}

// statements splits sanitized SQL on semicolons and collapses whitespace.
func statements(sanitized string) []string {
	var stmts []string
	for _, raw := range strings.Split(sanitized, ";") {
		if s := strings.Join(strings.Fields(raw), " "); s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}

// lintStatement reports forbidden constructs in one sanitized statement.
func lintStatement(stmt string) []string {
	var problems []string
	for _, p := range forbiddenPatterns {
		if m := p.re.FindString(stmt); m != "" {
			problems = append(problems, fmt.Sprintf("%s (%q)", p.what, m))
		}
	}
	isPolicy := regexp.MustCompile(`(?i)^CREATE\s+POLICY\b`).MatchString(stmt)
	for _, loc := range identParenRe.FindAllStringSubmatchIndex(stmt, -1) {
		word := strings.ToUpper(stmt[loc[2]:loc[3]])
		if parenWords[word] || (isPolicy && policyFunctions[word]) {
			continue
		}
		prev := wordRe.FindAllString(stmt[:loc[2]], -1)
		if len(prev) > 0 && tableNameWords[strings.ToUpper(prev[len(prev)-1])] {
			continue
		}
		problems = append(problems, fmt.Sprintf("function call or expression %q", stmt[loc[0]:loc[1]]))
	}
	if idx := regexp.MustCompile(`(?i)^CREATE\s+(?:UNIQUE\s+)?INDEX\b.*?\bON\s+\S+\s*(?:USING\s+\w+\s*)?\(`).FindStringIndex(stmt); idx != nil {
		depth := 1
		for _, r := range stmt[idx[1]:] {
			if r == '(' {
				problems = append(problems, "expression index")
				break
			}
			if r == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
	}
	return problems
}

// namedObject is a constraint or index created by a migration statement.
type namedObject struct {
	name, prefix, table string
}

// constraintPrefix maps a constraint keyword to the prefix its name must carry.
func constraintPrefix(keyword string) string {
	switch strings.ToUpper(strings.Fields(keyword)[0]) {
	case "PRIMARY":
		return "pk"
	case "UNIQUE":
		return "ux"
	case "CHECK":
		return "ck"
	default:
		return "fk"
	}
}

// namedObjects lists the constraints of a CREATE TABLE or ALTER TABLE … ADD statement and the
// index of a CREATE INDEX statement. A constraint without CONSTRAINT <name> is reported, because
// PostgreSQL would invent a name (x_pkey, x_tenant_id_id_key, x_check1) that Go cannot rely on.
func namedObjects(stmt string) ([]namedObject, []string) {
	if m := createIndexRe.FindStringSubmatch(stmt); m != nil {
		prefix := "ix"
		if m[1] != "" {
			prefix = "ux"
		}
		return []namedObject{{name: m[2], prefix: prefix, table: m[3]}}, nil
	}
	if regexp.MustCompile(`(?i)^CREATE\s+(?:UNIQUE\s+)?INDEX\b`).MatchString(stmt) {
		return nil, []string{"index without a name: " + stmt}
	}
	var table string
	if m := createTableRe.FindStringSubmatch(stmt); m != nil {
		table = m[1]
	} else if m := alterTableRe.FindStringSubmatch(stmt); m != nil {
		table = m[1]
	} else {
		return nil, nil
	}
	var objects []namedObject
	var problems []string
	for _, loc := range constraintRe.FindAllStringSubmatchIndex(stmt, -1) {
		keyword := stmt[loc[4]:loc[5]]
		if strings.EqualFold(keyword, "REFERENCES") && fkClauseEndRe.MatchString(stmt[:loc[4]]) {
			continue // the REFERENCES of a FOREIGN KEY (…) clause, named with the clause
		}
		if loc[2] < 0 {
			problems = append(problems, fmt.Sprintf("table %s: unnamed %s constraint; write CONSTRAINT <name> %s",
				table, strings.ToUpper(keyword), strings.ToUpper(keyword)))
			continue
		}
		objects = append(objects, namedObject{name: stmt[loc[2]:loc[3]], prefix: constraintPrefix(keyword), table: table})
	}
	return objects, problems
}

// checkName reports a name that does not follow pk_<table>, fk|ux|ck|ix_<table>__<rule>.
func checkName(o namedObject) []string {
	var problems []string
	if len(o.name) > maxIdentifier {
		problems = append(problems, fmt.Sprintf("%s is %d characters; PostgreSQL truncates names over %d", o.name, len(o.name), maxIdentifier))
	}
	if o.prefix == "pk" {
		if o.name != "pk_"+o.table {
			problems = append(problems, fmt.Sprintf("primary key of %s must be named pk_%s, got %s", o.table, o.table, o.name))
		}
		return problems
	}
	head := o.prefix + "_" + o.table + "__"
	if !strings.HasPrefix(o.name, head) || !nameTailRe.MatchString(strings.TrimPrefix(o.name, head)) {
		problems = append(problems, fmt.Sprintf("%s on %s must be named %s<rule> (lowercase words joined by _)", o.name, o.table, head))
	}
	return problems
}

// lintFile reports every problem of one migration file and the constraints and indexes its up
// file creates. tenantTables holds every table of the whole set that is under RLS.
func lintFile(name, sql string, isUp bool, tenantTables map[string]bool) ([]string, []namedObject) {
	clean, problems := sanitize(sql)
	stmts := statements(clean)
	if len(stmts) == 0 || !searchPathRe.MatchString(stmts[0]) {
		problems = append(problems, "first statement must be SET search_path = tally;")
	}
	var created []namedObject
	for _, s := range stmts {
		problems = append(problems, lintStatement(s)...)
		if !isUp {
			continue
		}
		objects, unnamed := namedObjects(s)
		problems = append(problems, unnamed...)
		for _, o := range objects {
			problems = append(problems, checkName(o)...)
		}
		created = append(created, objects...)
		if m := createIndexRe.FindStringSubmatch(s); m != nil && tenantTables[m[3]] && !strings.EqualFold(m[4], "tenant_id") {
			if _, ok := unledTenantIndexes[m[2]]; !ok {
				problems = append(problems, fmt.Sprintf("index %s on tenant table %s must lead with tenant_id", m[2], m[3]))
			}
		}
	}
	if isUp {
		joined := strings.Join(stmts, ";\n") + ";"
		for _, s := range stmts {
			m := createTableRe.FindStringSubmatch(s)
			if m == nil || !tenantColRe.MatchString(s) || slices.Contains(platformTables, m[1]) {
				continue
			}
			for _, want := range []string{
				"ALTER TABLE " + m[1] + " ENABLE ROW LEVEL SECURITY;",
				"ALTER TABLE " + m[1] + " FORCE ROW LEVEL SECURITY;",
				"CREATE POLICY tenant_isolation ON " + m[1] + " ",
			} {
				if !strings.Contains(joined, want) {
					problems = append(problems, fmt.Sprintf("tenant table %s lacks %q", m[1], strings.TrimSpace(want)))
				}
			}
		}
	}
	for i := range problems {
		problems[i] = name + ": " + problems[i]
	}
	return problems, created
}

// rlsTables lists the tables created with a tenant_id column that are not platform tables.
func rlsTables(files map[string]string) map[string]bool {
	tables := map[string]bool{}
	for _, sql := range files {
		clean, _ := sanitize(sql)
		for _, s := range statements(clean) {
			if m := createTableRe.FindStringSubmatch(s); m != nil && tenantColRe.MatchString(s) && !slices.Contains(platformTables, m[1]) {
				tables[m[1]] = true
			}
		}
	}
	return tables
}

// lintMigrations checks naming, up/down pairing, contiguous versions and every file's content.
func lintMigrations(fsys fs.FS) []string {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return []string{"read migrations: " + err.Error()}
	}
	var problems []string
	type pair struct{ up, down bool }
	versions := map[int]*pair{}
	names := map[int]string{}
	ups := map[string]string{}
	for _, e := range entries {
		if m := fileNameRe.FindStringSubmatch(e.Name()); m != nil && m[3] == "up" {
			data, err := fs.ReadFile(fsys, e.Name())
			if err != nil {
				return []string{e.Name() + ": " + err.Error()}
			}
			ups[e.Name()] = string(data)
		}
	}
	tenantTables := rlsTables(ups)
	owners := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		m := fileNameRe.FindStringSubmatch(name)
		if m == nil {
			problems = append(problems, name+": name must match NNNNNN_<domain>_<subject>.(up|down).sql")
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if prev, ok := names[v]; ok && prev != m[2] {
			problems = append(problems, fmt.Sprintf("version %d has two names: %s and %s", v, prev, m[2]))
		}
		names[v] = m[2]
		if versions[v] == nil {
			versions[v] = &pair{}
		}
		isUp := m[3] == "up"
		if isUp {
			versions[v].up = true
		} else {
			versions[v].down = true
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}
		fileProblems, created := lintFile(name, string(data), isUp, tenantTables)
		problems = append(problems, fileProblems...)
		for _, o := range created {
			if prev, ok := owners[o.name]; ok {
				problems = append(problems, fmt.Sprintf("%s: %s is already created by %s", name, o.name, prev))
				continue
			}
			owners[o.name] = name
		}
	}
	if len(versions) == 0 {
		return append(problems, "no migrations found")
	}
	for v := 1; v <= len(versions); v++ {
		p, ok := versions[v]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("version %06d is missing (versions must be contiguous from 1)", v))
		case !p.up:
			problems = append(problems, fmt.Sprintf("version %06d has no .up.sql", v))
		case !p.down:
			problems = append(problems, fmt.Sprintf("version %06d has no .down.sql", v))
		}
	}
	return problems
}

func TestMigrationsLint(t *testing.T) {
	for _, p := range lintMigrations(migrations.FS) {
		t.Error(p)
	}
}

func TestLintRejectsForbiddenSQL(t *testing.T) {
	cases := map[string]string{
		"function":               "CREATE FUNCTION f() RETURNS int AS 'select 1' LANGUAGE sql",
		"or replace function":    "create or replace function f() returns trigger as $$ begin end $$ language plpgsql",
		"procedure":              "CREATE PROCEDURE p() AS 'x' LANGUAGE sql",
		"trigger":                "CREATE TRIGGER t BEFORE UPDATE ON x FOR EACH ROW EXECUTE FUNCTION f()",
		"constraint trigger":     "CREATE CONSTRAINT TRIGGER t AFTER INSERT ON x FOR EACH ROW EXECUTE FUNCTION f()",
		"event trigger":          "CREATE EVENT TRIGGER t ON ddl_command_start EXECUTE FUNCTION f()",
		"rule":                   "CREATE RULE r AS ON INSERT TO x DO INSTEAD NOTHING",
		"aggregate":              "CREATE AGGREGATE a (int) (sfunc = int4pl, stype = int)",
		"extension":              "CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"enum type":              "CREATE TYPE status AS ENUM ('a','b')",
		"domain":                 "CREATE DOMAIN money_amount AS numeric(19,4)",
		"sequence":               "CREATE SEQUENCE s",
		"view":                   "CREATE VIEW v AS SELECT 1",
		"materialized view":      "CREATE MATERIALIZED VIEW v AS SELECT 1",
		"do block":               "DO $$ BEGIN PERFORM 1; END $$",
		"tagged do block":        "DO $body$ BEGIN END $body$",
		"quoted do block":        "DO 'BEGIN END'",
		"default now":            "CREATE TABLE x (id uuid PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())",
		"default uuid":           "CREATE TABLE x (id uuid PRIMARY KEY DEFAULT gen_random_uuid())",
		"default current time":   "CREATE TABLE x (id uuid PRIMARY KEY, created_at timestamptz DEFAULT CURRENT_TIMESTAMP)",
		"default nextval":        "ALTER TABLE x ALTER COLUMN n SET DEFAULT nextval('s')",
		"serial":                 "CREATE TABLE x (id bigserial PRIMARY KEY)",
		"identity":               "CREATE TABLE x (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY)",
		"generated column":       "CREATE TABLE x (a int, b int GENERATED ALWAYS AS (a * 2) STORED)",
		"expression index":       "CREATE INDEX ix_x__name ON x (tenant_id, lower(name))",
		"bare expression index":  "CREATE INDEX ix_x__sum ON x ((a + b))",
		"function in check":      "CREATE TABLE x (attributes jsonb NOT NULL, CHECK (jsonb_typeof(attributes) = 'object'))",
		"setting outside policy": "CREATE TABLE x (a uuid, CHECK (a = current_setting('app.tenant_id')::uuid))",
		"select call":            "SELECT set_config('search_path', 'public', false)",
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			clean, problems := sanitize(sql)
			for _, s := range statements(clean) {
				problems = append(problems, lintStatement(s)...)
			}
			if len(problems) == 0 {
				t.Errorf("not rejected: %s", sql)
			}
		})
	}
}

func TestLintAcceptsAllowedSQL(t *testing.T) {
	cases := map[string]string{
		"policy": `CREATE POLICY tenant_isolation ON x
			USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
			WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)`,
		"regex check":        `CREATE TABLE x (slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}$' AND slug !~ '--'))`,
		"partial unique":     `CREATE UNIQUE INDEX ux_x__one_open ON x (tenant_id) WHERE status = 'open'`,
		"nulls not distinct": `CREATE TABLE x (tenant_id uuid, a uuid, b uuid, UNIQUE NULLS NOT DISTINCT (tenant_id, a, b))`,
		"nnd partial index":  `CREATE UNIQUE INDEX ux_x__p ON x (tenant_id, a, b) NULLS NOT DISTINCT WHERE role = 'primary'`,
		"gin jsonb_path_ops": `CREATE INDEX ix_x__attrs ON x USING gin (attributes jsonb_path_ops)`,
		"constant defaults": `CREATE TABLE x (a numeric(19,4) NOT NULL DEFAULT 0, b bool DEFAULT false, c text DEFAULT 'draft',
			d jsonb NOT NULL DEFAULT '{}', e text[] NOT NULL DEFAULT '{}', f char(3) DEFAULT 'VND')`,
		"composite fk": `ALTER TABLE x ADD CONSTRAINT fk_x__y_id FOREIGN KEY (tenant_id, y_id) REFERENCES y (tenant_id, id)`,
		"arithmetic check": `CREATE TABLE x (qty numeric(18,4), f numeric(18,6), base numeric(18,4),
			CHECK (base = qty * f), CHECK ((status = 'done') = (done_at IS NOT NULL)), CHECK (a IN ('x','y')))`,
		"words in comments and literals": `-- CREATE FUNCTION f() in a comment, DEFAULT now()
			/* DO $$ nested /* comment */ $$ */
			INSERT INTO plans (id, name) VALUES ('00000000-0000-7000-8000-000000000001', 'now() serial LANGUAGE')`,
		"grants": `REVOKE UPDATE, DELETE ON a, b FROM tally_app`,
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			clean, problems := sanitize(sql)
			for _, s := range statements(clean) {
				problems = append(problems, lintStatement(s)...)
			}
			if len(problems) > 0 {
				t.Errorf("rejected allowed SQL: %v", problems)
			}
		})
	}
}

func TestLintChecksFileStructure(t *testing.T) {
	const head = "SET search_path = tally;\n"
	const rls = `ALTER TABLE x ENABLE ROW LEVEL SECURITY;
ALTER TABLE x FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON x
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
`
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	const table = "CREATE TABLE x (id uuid CONSTRAINT pk_x PRIMARY KEY, tenant_id uuid NOT NULL);\n"
	valid := fstest.MapFS{
		"000001_core_x.up.sql": file(head + `CREATE TABLE x (
  id uuid CONSTRAINT pk_x PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_x__tenant_id REFERENCES tenants (id),
  y_id uuid, qty numeric(18,4) NOT NULL CONSTRAINT ck_x__qty CHECK (qty > 0),
  CONSTRAINT ux_x__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_x__y_id FOREIGN KEY (tenant_id, y_id) REFERENCES y (tenant_id, id)
);
CREATE INDEX ix_x__y ON x (tenant_id, y_id);
CREATE UNIQUE INDEX ux_x__one_y ON x (tenant_id) WHERE y_id IS NULL;
` + rls),
		"000001_core_x.down.sql": file(head + "DROP TABLE x;\n"),
		"000002_core_z.up.sql":   file(head + "ALTER TABLE x ADD CONSTRAINT ck_x__y_id CHECK (y_id IS NOT NULL);\n"),
		"000002_core_z.down.sql": file(head + "ALTER TABLE x DROP CONSTRAINT ck_x__y_id;\n"),
	}
	if p := lintMigrations(valid); len(p) > 0 {
		t.Fatalf("valid set rejected: %v", p)
	}
	// withUp is a one-version set whose up file is the tenant table x followed by extra.
	withUp := func(extra string) fstest.MapFS {
		return fstest.MapFS{
			"000001_core_x.up.sql":   file(head + table + rls + extra),
			"000001_core_x.down.sql": file(head + "DROP TABLE x;\n"),
		}
	}
	if p := lintMigrations(withUp("")); len(p) > 0 {
		t.Fatalf("base set of the cases below rejected: %v", p)
	}
	cases := map[string]fstest.MapFS{
		"missing search_path": {
			"000001_core_x.up.sql":   file("CREATE TABLE y (id uuid PRIMARY KEY);\n"),
			"000001_core_x.down.sql": file(head + "DROP TABLE y;\n"),
		},
		"missing down": {
			"000001_core_x.up.sql": file(head + "CREATE TABLE y (id uuid PRIMARY KEY);\n"),
		},
		"version gap": {
			"000001_core_x.up.sql":   file(head),
			"000001_core_x.down.sql": file(head),
			"000003_core_y.up.sql":   file(head),
			"000003_core_y.down.sql": file(head),
		},
		"bad name": {
			"1_Core.up.sql": file(head),
		},
		"tenant table without rls": {
			"000001_core_x.up.sql":   file(head + "CREATE TABLE x (id uuid PRIMARY KEY, tenant_id uuid NOT NULL);\n"),
			"000001_core_x.down.sql": file(head + "DROP TABLE x;\n"),
		},
		"unnamed primary key": {
			"000001_core_x.up.sql":   file(head + "CREATE TABLE y (id uuid PRIMARY KEY);\n"),
			"000001_core_x.down.sql": file(head + "DROP TABLE y;\n"),
		},
		"unnamed column reference":  withUp("ALTER TABLE x ADD COLUMN y_id uuid REFERENCES y (id);\n"),
		"unnamed table check":       withUp("ALTER TABLE x ADD CHECK (id IS NOT NULL);\n"),
		"unnamed unique":            withUp("CREATE TABLE y (id uuid CONSTRAINT pk_y PRIMARY KEY, code text UNIQUE);\n"),
		"unnamed foreign key":       withUp("ALTER TABLE x ADD FOREIGN KEY (tenant_id, id) REFERENCES y (tenant_id, id);\n"),
		"primary key misnamed":      withUp("CREATE TABLE y (id uuid CONSTRAINT y_pkey PRIMARY KEY);\n"),
		"wrong prefix":              withUp("ALTER TABLE x ADD CONSTRAINT ux_x__id CHECK (id IS NOT NULL);\n"),
		"other table in name":       withUp("ALTER TABLE x ADD CONSTRAINT ck_y__id CHECK (id IS NOT NULL);\n"),
		"name without rule":         withUp("ALTER TABLE x ADD CONSTRAINT ck_x__ CHECK (id IS NOT NULL);\n"),
		"name longer than 63 bytes": withUp("ALTER TABLE x ADD CONSTRAINT ck_x__" + strings.Repeat("a", 60) + " CHECK (id IS NOT NULL);\n"),
		"duplicate name":            withUp("ALTER TABLE x ADD CONSTRAINT ck_x__id CHECK (id IS NOT NULL);\nALTER TABLE x ADD CONSTRAINT ck_x__id CHECK (id IS NOT NULL);\n"),
		"unnamed index":             withUp("CREATE INDEX ON x (tenant_id);\n"),
		"unique index named ix":     withUp("CREATE UNIQUE INDEX ix_x__id ON x (tenant_id, id);\n"),
		"index not led by tenant":   withUp("CREATE INDEX ix_x__id ON x (id, tenant_id);\n"),
		"tenant table without force": {
			"000001_core_x.up.sql": file(head + "CREATE TABLE x (id uuid PRIMARY KEY, tenant_id uuid NOT NULL);\n" +
				strings.Replace(rls, "ALTER TABLE x FORCE ROW LEVEL SECURITY;\n", "", 1)),
			"000001_core_x.down.sql": file(head + "DROP TABLE x;\n"),
		},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if p := lintMigrations(fsys); len(p) == 0 {
				t.Error("not rejected")
			}
		})
	}
}
