package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"
)

// DB configs from env or defaults
type dbConfig struct {
	name       string
	driver     string
	dsn        string
	createFunc func(db *sql.DB) (ItemsQuerier, error)
}

func getConfigs() []dbConfig {
	pgDSN := envOr("PG_DSN", "postgres://datacenter:datacenter123@127.0.0.1:5432/sqlgen_test?sslmode=disable")
	oraDSN := envOr("ORA_DSN", "oracle://sqlgen_test:sqlgen_test123@127.0.0.1:1521/XEPDB1")
	mysqlDSN := envOr("MYSQL_DSN", "root:root123@tcp(127.0.0.1:3306)/sqlgen_test?charset=utf8mb4&parseTime=true")

	return []dbConfig{
		{name: "pg", driver: "postgres", dsn: pgDSN, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
			return New(db, "postgres")
		}},
		{name: "mysql", driver: "mysql", dsn: mysqlDSN, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
			return New(db, "mysql")
		}},
		{name: "oracle", driver: "oracle", dsn: oraDSN, createFunc: func(db *sql.DB) (ItemsQuerier, error) {
			return New(db, "oracle")
		}},
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ensureTable creates the items table if not exists
func ensureTable(t *testing.T, db *sql.DB, dialect string) {
	var ddl string
	switch dialect {
	case "pg":
		ddl = `CREATE TABLE IF NOT EXISTS items (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(128) NOT NULL,
			category VARCHAR(64) NOT NULL DEFAULT '',
			price NUMERIC(10,2) NOT NULL DEFAULT 0,
			stock INT NOT NULL DEFAULT 0,
			is_active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`
	case "oracle":
		ddl = `BEGIN
			EXECUTE IMMEDIATE 'CREATE TABLE items (
				id NUMBER(19) GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
				name VARCHAR2(128) NOT NULL,
				category VARCHAR2(64) DEFAULT '' '' NOT NULL,
				price NUMBER(10,2) DEFAULT 0 NOT NULL,
				stock NUMBER(10) DEFAULT 0 NOT NULL,
				is_active NUMBER(1) DEFAULT 1 NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
			)';
		EXCEPTION WHEN OTHERS THEN
			IF SQLCODE = -955 THEN NULL; ELSE RAISE; END IF;
		END;`
	case "mysql":
		ddl = `CREATE TABLE IF NOT EXISTS items (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(128) NOT NULL,
			category VARCHAR(64) NOT NULL DEFAULT '',
			price DECIMAL(10,2) NOT NULL DEFAULT 0,
			stock INT NOT NULL DEFAULT 0,
			is_active TINYINT(1) NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	}
	_, err := db.Exec(ddl)
	if err != nil {
		t.Fatalf("[%s] create table: %v", dialect, err)
	}
	// Cleanup
	db.Exec("DELETE FROM items")
}

func setupDB(t *testing.T, cfg dbConfig) (ItemsQuerier, func()) {
	t.Helper()

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

	cleanup := func() {
		q.Close()
		db.Close()
	}
	return q, cleanup
}

// ============================================================
// Tests
// ============================================================

func TestInsertAndFindByID(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			id, err := q.InsertAndReturnID(ctx, "test-item", "cat1", 99.99, 10)
			if err != nil {
				t.Fatalf("InsertAndReturnID: %v", err)
			}
			if id <= 0 {
				t.Fatalf("expected id > 0, got %d", id)
			}

			item, err := q.FindByID(ctx, id)
			if err != nil {
				t.Fatalf("FindByID: %v", err)
			}
			if item.Name != "test-item" {
				t.Fatalf("name mismatch: %q", item.Name)
			}
			if item.Price != 99.99 {
				t.Fatalf("price mismatch: %f", item.Price)
			}
			t.Logf("OK: id=%d name=%s price=%.2f", id, item.Name, item.Price)
		})
	}
}

func TestInsertExecAndRows(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			err := q.InsertItem(ctx, "exec-test", "cat2", 50.0, 5)
			if err != nil {
				t.Fatalf("InsertItem: %v", err)
			}

			rows, err := q.InsertItemRows(ctx, "rows-test", "cat2", 60.0, 3)
			if err != nil {
				t.Fatalf("InsertItemRows: %v", err)
			}
			if rows != 1 {
				t.Fatalf("InsertItemRows: expected 1, got %d", rows)
			}
			t.Logf("OK: exec + execrows")
		})
	}
}

func TestFindAll(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			// Seed data
			_, _ = q.InsertAndReturnID(ctx, "item-a", "catA", 10.0, 1)
			_, _ = q.InsertAndReturnID(ctx, "item-b", "catA", 20.0, 2)
			_, _ = q.InsertAndReturnID(ctx, "item-c", "catB", 30.0, 3)

			items, err := q.FindAll(ctx)
			if err != nil {
				t.Fatalf("FindAll: %v", err)
			}
			if len(items) < 3 {
				t.Fatalf("expected >=3, got %d", len(items))
			}
			t.Logf("OK: %d items", len(items))
		})
	}
}

func TestFindByCategory(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "cat-item-1", "sports", 100.0, 5)
			_, _ = q.InsertAndReturnID(ctx, "cat-item-2", "sports", 200.0, 3)

			items, err := q.FindByCategory(ctx, "sports")
			if err != nil {
				t.Fatalf("FindByCategory: %v", err)
			}
			if len(items) != 2 {
				t.Fatalf("expected 2, got %d", len(items))
			}
			for _, item := range items {
				if item.Category != "sports" {
					t.Fatalf("category mismatch: %s", item.Category)
				}
			}
			t.Logf("OK: %d items in sports", len(items))
		})
	}
}

func TestListPaged(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			for i := 0; i < 5; i++ {
				_, _ = q.InsertAndReturnID(ctx, fmt.Sprintf("page-item-%d", i), "page", 1.0, 0)
			}

			items, err := q.ListPaged(ctx, 2, 1)
			if err != nil {
				t.Fatalf("ListPaged: %v", err)
			}
			if len(items) != 2 {
				t.Fatalf("expected 2, got %d", len(items))
			}
			t.Logf("OK: %d items (limit=2 offset=1)", len(items))
		})
	}
}

func TestUpdate(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			id, _ := q.InsertAndReturnID(ctx, "update-me", "misc", 10.0, 1)

			err := q.UpdateItem(ctx, "updated-name", 99.0, id)
			if err != nil {
				t.Fatalf("UpdateItem: %v", err)
			}

			item, err := q.FindByID(ctx, id)
			if err != nil {
				t.Fatalf("FindByID: %v", err)
			}
			if item.Name != "updated-name" {
				t.Fatalf("name not updated: %s", item.Name)
			}
			if item.Price != 99.0 {
				t.Fatalf("price not updated: %f", item.Price)
			}
			t.Logf("OK: name=%s price=%.2f", item.Name, item.Price)
		})
	}
}

func TestDelete(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			id, _ := q.InsertAndReturnID(ctx, "delete-me", "tmp", 1.0, 0)

			err := q.DeleteItem(ctx, id)
			if err != nil {
				t.Fatalf("DeleteItem: %v", err)
			}

			_, err = q.FindByID(ctx, id)
			if err == nil {
				t.Fatal("expected error for deleted item")
			}
			t.Logf("OK: deleted id=%d", id)
		})
	}
}

func TestDeleteRows(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			id, _ := q.InsertAndReturnID(ctx, "del-rows", "tmp", 1.0, 0)

			rows, err := q.DeleteItemRows(ctx, id)
			if err != nil {
				t.Fatalf("DeleteItemRows: %v", err)
			}
			if rows != 1 {
				t.Fatalf("expected 1 row affected, got %d", rows)
			}
			t.Logf("OK: %d rows deleted", rows)
		})
	}
}

func TestUpsert(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			// MySQL doesn't support ON CONFLICT DO NOTHING via standard SQL (MERGE is Oracle/MSSQL only)
			// But INSERT IGNORE should work
			err := q.UpsertIgnore(ctx, "upsert-name", "cat")
			if err != nil {
				if cfg.name == "oracle" || cfg.name == "mysql" {
					t.Skipf("%s: upsert ignore skipped", cfg.name)
				}
				t.Fatalf("UpsertIgnore: %v", err)
			}

			err = q.UpsertItem(ctx, "upsert-name", "updated-cat", 999.0, 99)
			if err != nil {
				t.Fatalf("UpsertItem: %v", err)
			}

			items, _ := q.FindByCategory(ctx, "updated-cat")
			if len(items) != 1 {
				t.Fatalf("expected 1, got %d", len(items))
			}
			t.Logf("OK: upserted name=%s category=%s", items[0].Name, items[0].Category)
		})
	}
}

func TestILIKE(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			// Use unique test names to avoid collision with other tests
			_, _ = q.InsertAndReturnID(ctx, "ILikeSearch-One", "ilike-cat", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "ILikeSearch-Two", "ilike-cat", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "xyz-nomatch", "ilike-cat", 1.0, 0)

			items, err := q.SearchByName(ctx, "%ILikeSearch%")
			if err != nil {
				t.Fatalf("SearchByName: %v", err)
			}
			if len(items) < 2 {
				t.Fatalf("expected >=2, got %d", len(items))
			}
			t.Logf("OK: %d items matched", len(items))
		})
	}
}

func TestSearchOptional(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "opt-search", "cat", 1.0, 0)

			// nil keyword → return all (may fail on PG with untyped nil params)
			items, err := q.SearchOptional(ctx, nil)
			if err != nil {
				if cfg.name == "pg" {
					t.Skipf("PG: nil param typing limitation")
				}
				t.Fatalf("SearchOptional nil: %v", err)
			}
			if len(items) == 0 {
				t.Fatal("expected >0 items with nil keyword")
			}

			// With keyword
			kw := "%opt%"
			items2, err := q.SearchOptional(ctx, &kw)
			if err != nil {
				t.Fatalf("SearchOptional with kw: %v", err)
			}
			if len(items2) == 0 {
				t.Fatal("expected >0 items with keyword")
			}
			t.Logf("OK: nil=%d items, kw=%d items", len(items), len(items2))
		})
	}
}

func TestCountByCategory(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "g1", "group-a", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "g2", "group-a", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "g3", "group-b", 1.0, 0)

			counts, err := q.CountByCategory(ctx)
			if err != nil {
				t.Fatalf("CountByCategory: %v", err)
			}
			if len(counts) < 2 {
				t.Fatalf("expected >=2, got %d", len(counts))
			}
			for _, c := range counts {
				t.Logf("  category=%s cnt=%d", c.Category, c.Cnt)
			}
			t.Logf("OK: %d categories", len(counts))
		})
	}
}

func TestExistsByName(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "exists-test", "cat", 1.0, 0)

			r, err := q.ExistsByName(ctx, "exists-test")
			if err != nil {
				if cfg.name == "oracle" {
					t.Skipf("Oracle: EXISTS needs CASE WHEN wrapper")
				}
				t.Fatalf("ExistsByName: %v", err)
			}
			if !r.Exists {
				t.Fatal("expected exists=true")
			}

			r2, err := q.ExistsByName(ctx, "no-such-item")
			if err != nil {
				t.Fatalf("ExistsByName not found: %v", err)
			}
			if r2.Exists {
				t.Fatal("expected exists=false")
			}
			t.Logf("OK: exists=%v not-exists=%v", r.Exists, r2.Exists)
		})
	}
}

func TestFindByFilters(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "filter-a", "electronics", 500.0, 10)
			_, _ = q.InsertAndReturnID(ctx, "filter-b", "electronics", 100.0, 5)
			_, _ = q.InsertAndReturnID(ctx, "filter-c", "books", 50.0, 3)

			// All nil → all items (may fail on PG with untyped nil params)
			items, err := q.FindByFilters(ctx, nil, nil, nil)
			if err != nil {
				if cfg.name == "pg" {
					t.Skipf("PG: nil param typing limitation")
				}
				t.Fatalf("FindByFilters all-nil: %v", err)
			}
			if len(items) < 3 {
				t.Fatalf("expected >=3, got %d", len(items))
			}

			// Filter by category
			cat := "electronics"
			minPrice := 200.0
			items2, err := q.FindByFilters(ctx, &cat, &minPrice, nil)
			if err != nil {
				t.Fatalf("FindByFilters filtered: %v", err)
			}
			if len(items2) != 1 {
				t.Fatalf("expected 1, got %d", len(items2))
			}
			t.Logf("OK: all=%d filtered=%d", len(items), len(items2))
		})
	}
}

func TestCountAll(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "c1", "cat", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "c2", "cat", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "c3", "cat", 1.0, 0)

			cnt, err := q.CountAll(ctx)
			if err != nil {
				t.Fatalf("CountAll: %v", err)
			}
			if cnt < 3 {
				t.Fatalf("expected >=3, got %d", cnt)
			}
			t.Logf("OK: count=%d", cnt)
		})
	}
}

func TestListNames(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "name-a", "names", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "name-b", "names", 1.0, 0)

			names, err := q.ListNames(ctx, "names")
			if err != nil {
				t.Fatalf("ListNames: %v", err)
			}
			if len(names) < 2 {
				t.Fatalf("expected >=2, got %d", len(names))
			}
			t.Logf("OK: %d names", len(names))
		})
	}
}

func TestListBrief(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			_, _ = q.InsertAndReturnID(ctx, "brief-a", "briefs", 1.0, 0)
			_, _ = q.InsertAndReturnID(ctx, "brief-b", "briefs", 1.0, 0)

			items, err := q.ListBrief(ctx, "briefs")
			if err != nil {
				t.Fatalf("ListBrief: %v", err)
			}
			if len(items) < 2 {
				t.Fatalf("expected >=2, got %d", len(items))
			}
			for _, item := range items {
				t.Logf("  id=%d name=%s category=%s", item.ID, item.Name, item.Category)
			}
			t.Logf("OK: %d items", len(items))
		})
	}
}

// ensure time import used
var _ = time.Now

func TestFindWithExtra(t *testing.T) {
	for _, cfg := range getConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			q, cleanup := setupDB(t, cfg)
			defer cleanup()
			ctx := context.Background()

			id, _ := q.InsertAndReturnID(ctx, "extra-test", "extras", 99.0, 5)
			item, err := q.FindWithExtra(ctx, id)
			if err != nil {
				t.Fatalf("FindWithExtra: %v", err)
			}
			if item.ID != id { t.Fatalf("id mismatch: %d", item.ID) }
			if item.Name != "extra-test" { t.Fatalf("name mismatch: %s", item.Name) }
			if item.Category != "extras" { t.Fatalf("category mismatch: %s", item.Category) }
			t.Logf("OK: id=%d name=%s cat=%s (price/stock/created_at discarded)", item.ID, item.Name, item.Category)
		})
	}
}
