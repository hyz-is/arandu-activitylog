package unit_test

import (
	"reflect"
	"slices"
	"testing"

	activitylog "github.com/hyz-is/arandu-activitylog"
	"github.com/hyz-is/arandu-activitylog/internal/everytenant"
)

// columns are the db tags of a row type, in order.
func columns(row any) []string {
	var out []string
	kind := reflect.TypeOf(row)
	for i := range kind.NumField() {
		if tag := kind.Field(i).Tag.Get("db"); tag != "" {
			out = append(out, tag)
		}
	}
	slices.Sort(out)
	return out
}

// The row ReadAll reads is the activity table's: a column added to Activity
// and not to everytenant.Entry is a column the platform read never sees.
func TestThePlatformRowHasTheColumnsOfAnEntry(t *testing.T) {
	if got, want := columns(everytenant.Entry{}), columns(activitylog.Activity{}); !slices.Equal(got, want) {
		t.Fatalf("everytenant.Entry reads %v, Activity has %v", got, want)
	}
	if everytenant.Entries(nil) == nil {
		t.Fatal("no query on the platform row")
	}
	if activitylog.ActivityTable != "activity_log" {
		t.Fatalf("the table is %q, and everytenant names activity_log", activitylog.ActivityTable)
	}
}
