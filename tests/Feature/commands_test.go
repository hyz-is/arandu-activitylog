package feature_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/console"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// activitylog:clean is Spatie's: without --force it counts what it would
// remove, with --force it removes it, and a bad --days is refused.
func TestTheCleanCommandCountsThenRemoves(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	for _, at := range []time.Time{time.Now().AddDate(0, 0, -400), time.Now().AddDate(0, 0, -400), time.Now()} {
		if _, err := logger.Activity(ctx, g).CreatedAt(at).Log("entry"); err != nil {
			t.Fatal(err)
		}
	}
	commands, err := activitylog.Commands(activitylog.Deps{Service: svc, Operator: member})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	app := console.NewApplication(&out, &errOut, strings.NewReader("")).Add(commands...)

	if err := app.Call(ctx, "activitylog:clean", "--tenant=acme"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 record(s) would be deleted") {
		t.Errorf("dry run said %q", out.String())
	}
	if left := entries(t, svc, "acme", activitylog.Filter{}); len(left) != 3 {
		t.Fatalf("the dry run removed entries")
	}
	if err := app.Call(ctx, "activitylog:clean", "--tenant=acme", "--force"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Deleted 2 record(s) from the activity log.") {
		t.Errorf("clean said %q", out.String())
	}
	if left := entries(t, svc, "acme", activitylog.Filter{}); len(left) != 1 {
		t.Errorf("%d entries left, want 1", len(left))
	}
	if err := app.Call(ctx, "activitylog:clean", "--tenant=acme", "--days=0", "--force"); err == nil {
		t.Error("--days=0 was accepted")
	}
	if err := app.Call(ctx, "activitylog:clean", "--force"); err == nil {
		t.Error("a clean without --tenant was accepted")
	}
	if _, err := activitylog.Commands(activitylog.Deps{Service: svc}); err == nil {
		t.Error("commands without an operator were built")
	}
}
