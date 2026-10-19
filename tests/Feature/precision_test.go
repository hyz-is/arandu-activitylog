package feature_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/hesape/database"
	pgxconnector "github.com/arandu-io/hesape/database/connectors/pgx"
	"github.com/arandu-io/hesape/database/migrations"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// A log migrated before the framework's default time precision was
// microseconds holds its instants in whole seconds. The widening raises them
// in place, and entries written within one second are read back newest first
// in the order they were written.

func TestTheLastMigrationWidensTheLog(t *testing.T) {
	all := (&activitylog.Module{}).Migrations()
	widen, ok := all[len(all)-1].(migrations.WidenTimePrecision)
	if !ok || widen.GetName() != "20261019_0001_widen_activity_log_time_precision" ||
		len(widen.Tables) != 1 || widen.Tables[0] != activitylog.ActivityTable {
		t.Fatalf("the last migration is %s, want the widening of the log", all[len(all)-1].GetName())
	}
}

func TestTheLogIsWidenedAndKeepsItsOrderOnPostgres(t *testing.T) {
	dsn := os.Getenv("ARANDU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("no server: set ARANDU_TEST_POSTGRES_DSN")
	}
	ctx := context.Background()
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	schema := "activitylog_" + hex.EncodeToString(suffix)

	admin, err := sql.Open(pgxconnector.PostgresConnector{}.DriverName(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	raw, err := sql.Open(pgxconnector.PostgresConnector{}.DriverName(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	conn := database.ForMigrations(database.NewConnection(raw, schema, "", map[string]any{"driver": "pgsql", "name": "activitylog-test"}))
	all := (&activitylog.Module{}).Migrations()
	for _, migration := range all[:len(all)-1] {
		if err := migration.Up(ctx, conn); err != nil {
			t.Fatalf("applying %s: %v", migration.GetName(), err)
		}
	}
	// The shape a release before the widening left on a database migrated
	// while the default was whole seconds, with one entry already in it.
	for _, column := range []string{"created_at", "updated_at"} {
		if _, err := raw.ExecContext(ctx, `ALTER TABLE `+activitylog.ActivityTable+` ALTER COLUMN `+column+` TYPE timestamp(0) without time zone`); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if _, err := raw.ExecContext(ctx, `INSERT INTO `+activitylog.ActivityTable+` (id, tenant_id, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`, "old-entry", "acme", "before the widening", at, at); err != nil {
		t.Fatal(err)
	}
	if err := all[len(all)-1].Up(ctx, conn); err != nil {
		t.Fatalf("widening: %v", err)
	}
	var narrow int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND data_type LIKE 'timestamp%' AND datetime_precision < 6`).Scan(&narrow); err != nil {
		t.Fatal(err)
	}
	if narrow != 0 {
		t.Fatalf("%d date-time columns are still in seconds after the widening", narrow)
	}
	// New connections, as a deploy has after it migrates: one that prepared a
	// statement over the narrow columns is refused its cached plan.
	raw.SetMaxIdleConns(0)
	raw.SetMaxIdleConns(2)

	db := data.Wrap(raw, data.DialectPostgres)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	g := grantOf(t, member("acme"))
	var written []string
	for i := range 5 {
		entry, err := logger.In(ctx, g, "default").WithProperty("n", i).Log("entry")
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, entry.ID)
	}
	read, err := svc.List(ctx, member("acme"), activitylog.Filter{}, data.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 6 || read[5].ID != "old-entry" {
		t.Fatalf("read %d entries, want the five written and the one from before the widening last", len(read))
	}
	for i, id := range written {
		if read[4-i].ID != id {
			t.Fatalf("entry %d written is read at %d: the log is not newest first in the order it was written", i, 4-i)
		}
	}
	if read[0].CreatedAt.Before(read[4].CreatedAt) {
		t.Fatalf("the newest entry was created at %s, before the oldest at %s", read[0].CreatedAt, read[4].CreatedAt)
	}
}
