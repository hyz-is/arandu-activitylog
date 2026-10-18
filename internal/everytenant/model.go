// Package everytenant is the activity table read without the tenant filter:
// the one read of arandu-activitylog that crosses tenants,
// ActivityService.ReadAll, after the application's platform policy allowed
// it.
//
// It is internal so no application can import it. Its table is Global, which
// is how Hesape spells a table with no tenant filter, and any Grant at all
// would read every tenant through it; the package that may reach it is the
// one whose only caller has asked Config.PlatformPolicy first. Its rows never
// leave that caller, which copies them into snapshots holding no connection.
package everytenant

import (
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Entry is a row of the activity table, of any tenant. Its columns are
// those of activitylog.Activity.
type Entry struct {
	model.Model

	ID               string    `db:"id"`
	TenantID         string    `db:"tenant_id"`
	LogName          *string   `db:"log_name"`
	Description      string    `db:"description"`
	SubjectType      *string   `db:"subject_type"`
	SubjectID        *string   `db:"subject_id"`
	Event            *string   `db:"event"`
	CauserType       *string   `db:"causer_type"`
	CauserID         *string   `db:"causer_id"`
	OnBehalfOf       *string   `db:"on_behalf_of"`
	AttributeChanges *string   `db:"attribute_changes"`
	PropertiesJSON   *string   `db:"properties"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

// entryTable is the activity table, Global: no tenant filter on any
// statement. The name is written here rather than imported, because the
// package that owns the constant imports this one.
var entryTable = model.NewTable(model.TableSpec{
	Name:         "activity_log",
	New:          func() model.Entity { return new(Entry) },
	UniqueIDs:    true,
	NoTimestamps: true,
	Global:       true,
})
