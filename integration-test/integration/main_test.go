package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
)

type dbConfig struct {
	name       string
	driver     string
	dsn        string
	skip       bool
	createFunc func(db *sql.DB) (ItemsQuerier, error)
}

func pgConfig() dbConfig {
	dsn := envOr("PG_DSN", "postgres://datacenter:datacenter123@127.0.0.1:5432/sqlgen_test?sslmode=disable")
	return dbConfig{name: "pg", driver: "postgres", dsn: dsn, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
		return New(db, "postgres")
	}}
}

func mysqlConfig() dbConfig {
	dsn := envOr("MYSQL_DSN", "root:root123@tcp(127.0.0.1:3306)/sqlgen_test?charset=utf8mb4&parseTime=true")
	return dbConfig{name: "mysql", driver: "mysql", dsn: dsn, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
		return New(db, "mysql")
	}}
}

func oracleConfig() dbConfig {
	dsn := envOr("ORA_DSN", "oracle://sqlgen_test:sqlgen_test123@127.0.0.1:1521/XEPDB1")
	return dbConfig{name: "oracle", driver: "oracle", dsn: dsn, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
		return New(db, "oracle")
	}}
}

func mssqlConfig() dbConfig {
	return dbConfig{name: "mssql", driver: "sqlserver", dsn: "", skip: true, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
		return New(db, "sqlserver")
	}}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func ensureTable(t *testing.T, db *sql.DB, dialect string) {
	switch dialect {
	case "pg":
		db.Exec(`CREATE TABLE IF NOT EXISTS items (
			id BIGSERIAL PRIMARY KEY, name VARCHAR(128) NOT NULL,
			category VARCHAR(64) NOT NULL DEFAULT '', price NUMERIC(10,2) NOT NULL DEFAULT 0,
			stock INT NOT NULL DEFAULT 0, is_active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(), updated_at TIMESTAMP NOT NULL DEFAULT NOW())`)
	case "oracle":
		db.Exec(`BEGIN EXECUTE IMMEDIATE 'CREATE TABLE items (
			id NUMBER(19) GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			name VARCHAR2(128) NOT NULL, category VARCHAR2(64) DEFAULT '' '' NOT NULL,
			price NUMBER(10,2) DEFAULT 0 NOT NULL, stock NUMBER(10) DEFAULT 0 NOT NULL,
			is_active NUMBER(1) DEFAULT 1 NOT NULL, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL)';
		EXCEPTION WHEN OTHERS THEN IF SQLCODE = -955 THEN NULL; ELSE RAISE; END IF; END;`)
	case "mysql":
		db.Exec(`CREATE TABLE IF NOT EXISTS items (
			id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(128) NOT NULL,
			category VARCHAR(64) NOT NULL DEFAULT '', price DECIMAL(10,2) NOT NULL DEFAULT 0,
			stock INT NOT NULL DEFAULT 0, is_active TINYINT(1) NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP)
			ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	}
	db.Exec("DELETE FROM items")
}

func setupDB(t *testing.T, cfg dbConfig) (ItemsQuerier, func()) {
	t.Helper()
	if cfg.skip {
		t.Skipf("%s: no environment available", cfg.name)
	}

	db, err := sql.Open(cfg.driver, cfg.dsn)
	if err != nil {
		t.Fatalf("[%s] open: %v", cfg.name, err)
	}

	ensureTable(t, db, cfg.name)

	q, err := cfg.createFunc(db)
	if err != nil {
		db.Close()
		t.Fatalf("[%s] create querier: %v", cfg.name, err)
	}

	return q, func() { q.Close(); db.Close() }
}

// ============================================================
// Dialect entry points
// ============================================================

func TestPG(t *testing.T)     { runSuite(t, pgConfig()) }
func TestMySQL(t *testing.T)  { runSuite(t, mysqlConfig()) }
func TestOracle(t *testing.T) { runSuite(t, oracleConfig()) }
func TestMSSQL(t *testing.T)  { runSuite(t, mssqlConfig()) }

// ============================================================
// Shared test suite
// ============================================================

type testFunc func(t *testing.T, q ItemsQuerier, dialect string)

func runSuite(t *testing.T, cfg dbConfig) {
	q, cleanup := setupDB(t, cfg)
	defer cleanup()
	ctx := context.Background()

	tests := map[string]testFunc{
		"InsertAndFindByID":  testInsertAndFindByID,
		"InsertExecAndRows":  testInsertExecAndRows,
		"FindAll":            testFindAll,
		"FindByCategory":     testFindByCategory,
		"ListPaged":          testListPaged,
		"Update":             testUpdate,
		"Delete":             testDelete,
		"DeleteRows":         testDeleteRows,
		"Upsert":             testUpsert,
		"ILIKE":              testILIKE,
		"SearchOptional":     testSearchOptional,
		"CountByCategory":    testCountByCategory,
		"ExistsByName":       testExistsByName,
		"FindByFilters":      testFindByFilters,
		"CountAll":           testCountAll,
		"ListNames":          testListNames,
		"ListBrief":          testListBrief,
		"FindWithExtra":      testFindWithExtra,
	}

	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			fn(t, q, cfg.name)
		})
	}
	_ = ctx
}

func testInsertAndFindByID(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	id, err := q.InsertAndReturnID(ctx, "test-item", "cat1", 99.99, 10)
	if err != nil { t.Fatalf("InsertAndReturnID: %v", err) }
	if id <= 0 { t.Fatalf("expected id > 0, got %d", id) }
	item, err := q.FindByID(ctx, id)
	if err != nil { t.Fatalf("FindByID: %v", err) }
	if item.Name != "test-item" { t.Fatalf("name mismatch: %q", item.Name) }
	t.Logf("OK: id=%d name=%s", id, item.Name)
}

func testInsertExecAndRows(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	if err := q.InsertItem(ctx, "exec-test", "cat2", 50.0, 5); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	rows, err := q.InsertItemRows(ctx, "rows-test", "cat2", 60.0, 3)
	if err != nil { t.Fatalf("InsertItemRows: %v", err) }
	if rows != 1 { t.Fatalf("expected 1, got %d", rows) }
	t.Log("OK: exec + execrows")
}

func testFindAll(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "item-a", "catA", 10.0, 1)
	_, _ = q.InsertAndReturnID(ctx, "item-b", "catA", 20.0, 2)
	_, _ = q.InsertAndReturnID(ctx, "item-c", "catB", 30.0, 3)
	items, err := q.FindAll(ctx)
	if err != nil { t.Fatalf("FindAll: %v", err) }
	if len(items) < 3 { t.Fatalf("expected >=3, got %d", len(items)) }
	t.Logf("OK: %d items", len(items))
}

func testFindByCategory(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "cat-item-1", "sports", 100.0, 5)
	_, _ = q.InsertAndReturnID(ctx, "cat-item-2", "sports", 200.0, 3)
	items, err := q.FindByCategory(ctx, "sports")
	if err != nil { t.Fatalf("FindByCategory: %v", err) }
	if len(items) != 2 { t.Fatalf("expected 2, got %d", len(items)) }
	t.Logf("OK: %d items in sports", len(items))
}

func testListPaged(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = q.InsertAndReturnID(ctx, fmt.Sprintf("page-item-%d", i), "page", 1.0, 0)
	}
	items, err := q.ListPaged(ctx, 2, 1)
	if err != nil { t.Fatalf("ListPaged: %v", err) }
	if len(items) != 2 { t.Fatalf("expected 2, got %d", len(items)) }
	t.Logf("OK: %d items", len(items))
}

func testUpdate(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	id, _ := q.InsertAndReturnID(ctx, "update-me", "misc", 10.0, 1)
	if err := q.UpdateItem(ctx, "updated-name", 99.0, id); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	item, _ := q.FindByID(ctx, id)
	if item.Name != "updated-name" { t.Fatalf("name not updated: %s", item.Name) }
	t.Logf("OK: name=%s", item.Name)
}

func testDelete(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	id, _ := q.InsertAndReturnID(ctx, "delete-me", "tmp", 1.0, 0)
	if err := q.DeleteItem(ctx, id); err != nil { t.Fatalf("DeleteItem: %v", err) }
	if _, err := q.FindByID(ctx, id); err == nil { t.Fatal("expected error for deleted item") }
	t.Logf("OK: deleted id=%d", id)
}

func testDeleteRows(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	id, _ := q.InsertAndReturnID(ctx, "del-rows", "tmp", 1.0, 0)
	rows, err := q.DeleteItemRows(ctx, id)
	if err != nil { t.Fatalf("DeleteItemRows: %v", err) }
	if rows != 1 { t.Fatalf("expected 1, got %d", rows) }
	t.Logf("OK: %d rows deleted", rows)
}

func testUpsert(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_ = q.UpsertIgnore(ctx, "upsert-name", "cat")
	if err := q.UpsertItem(ctx, "upsert-name", "updated-cat", 999.0, 99); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	items, _ := q.FindByCategory(ctx, "updated-cat")
	if len(items) != 1 { t.Fatalf("expected 1, got %d", len(items)) }
	t.Logf("OK: name=%s cat=%s", items[0].Name, items[0].Category)
}

func testILIKE(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "ILikeSearch-One", "ilike-cat", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "ILikeSearch-Two", "ilike-cat", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "xyz-nomatch", "ilike-cat", 1.0, 0)
	items, err := q.SearchByName(ctx, "%ILikeSearch%")
	if err != nil { t.Fatalf("SearchByName: %v", err) }
	if len(items) < 2 { t.Fatalf("expected >=2, got %d", len(items)) }
	t.Logf("OK: %d items matched", len(items))
}

func testSearchOptional(t *testing.T, q ItemsQuerier, dialect string) {
	if dialect == "pg" { t.Skip("PG: nil param typing limitation") }
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "opt-search", "cat", 1.0, 0)
	kw := "%opt%"
	items, err := q.SearchOptional(ctx, &kw)
	if err != nil { t.Fatalf("SearchOptional: %v", err) }
	if len(items) == 0 { t.Fatal("expected >0 items") }
	t.Logf("OK: %d items", len(items))
}

func testCountByCategory(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "g1", "group-a", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "g2", "group-a", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "g3", "group-b", 1.0, 0)
	counts, err := q.CountByCategory(ctx)
	if err != nil { t.Fatalf("CountByCategory: %v", err) }
	if len(counts) < 2 { t.Fatalf("expected >=2, got %d", len(counts)) }
	t.Logf("OK: %d categories", len(counts))
}

func testExistsByName(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "exists-test", "cat", 1.0, 0)
	r, err := q.ExistsByName(ctx, "exists-test")
	if err != nil { t.Fatalf("ExistsByName: %v", err) }
	if !r.Exists { t.Fatal("expected exists=true") }
	r2, _ := q.ExistsByName(ctx, "no-such-item")
	if r2.Exists { t.Fatal("expected exists=false") }
	t.Logf("OK: exists=%v not-exists=%v", r.Exists, r2.Exists)
}

func testFindByFilters(t *testing.T, q ItemsQuerier, dialect string) {
	if dialect == "pg" { t.Skip("PG: nil param typing limitation") }
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "filter-a", "electronics", 500.0, 10)
	_, _ = q.InsertAndReturnID(ctx, "filter-b", "electronics", 100.0, 5)
	_, _ = q.InsertAndReturnID(ctx, "filter-c", "books", 50.0, 3)
	cat := "electronics"
	minPrice := 200.0
	items, err := q.FindByFilters(ctx, &cat, &minPrice, nil)
	if err != nil { t.Fatalf("FindByFilters: %v", err) }
	if len(items) != 1 { t.Fatalf("expected 1, got %d", len(items)) }
	t.Logf("OK: %d items", len(items))
}

func testCountAll(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "c1", "cat", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "c2", "cat", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "c3", "cat", 1.0, 0)
	cnt, err := q.CountAll(ctx)
	if err != nil { t.Fatalf("CountAll: %v", err) }
	if cnt < 3 { t.Fatalf("expected >=3, got %d", cnt) }
	t.Logf("OK: count=%d", cnt)
}

func testListNames(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "name-a", "names", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "name-b", "names", 1.0, 0)
	names, err := q.ListNames(ctx, "names")
	if err != nil { t.Fatalf("ListNames: %v", err) }
	if len(names) < 2 { t.Fatalf("expected >=2, got %d", len(names)) }
	t.Logf("OK: %d names", len(names))
}

func testListBrief(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	_, _ = q.InsertAndReturnID(ctx, "brief-a", "briefs", 1.0, 0)
	_, _ = q.InsertAndReturnID(ctx, "brief-b", "briefs", 1.0, 0)
	items, err := q.ListBrief(ctx, "briefs")
	if err != nil { t.Fatalf("ListBrief: %v", err) }
	if len(items) < 2 { t.Fatalf("expected >=2, got %d", len(items)) }
	t.Logf("OK: %d items", len(items))
}

func testFindWithExtra(t *testing.T, q ItemsQuerier, _ string) {
	ctx := context.Background()
	id, _ := q.InsertAndReturnID(ctx, "extra-test", "extras", 99.0, 5)
	item, err := q.FindWithExtra(ctx, id)
	if err != nil { t.Fatalf("FindWithExtra: %v", err) }
	if item.ID != id { t.Fatalf("id mismatch: %d", item.ID) }
	if item.Name != "extra-test" { t.Fatalf("name mismatch: %s", item.Name) }
	t.Logf("OK: id=%d name=%s (extra cols discarded)", item.ID, item.Name)
}

var _ = time.Now
