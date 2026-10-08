package activitylog

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// ActivityTable is the table this package owns. It is a constant because it is
// written in the migration, in the model and in the tests, and three spellings
// of one table disagree on the day one of them is changed.
const ActivityTable = "activity_log"

// The events a record's changes are logged under, as Spatie's ActivityEvent
// names them.
const (
	EventCreated  = "created"
	EventUpdated  = "updated"
	EventDeleted  = "deleted"
	EventRestored = "restored"
)

// DefaultEvents are the events a record logs when its options name none.
var DefaultEvents = []string{EventCreated, EventUpdated, EventDeleted, EventRestored}

// Activity is one entry of the log: something that happened, to what, by whom.
//
// It embeds the model, so a row returned by a query carries its connection.
// The two JSON columns are kept as text, which every engine stores the same
// way, and are read and written through the methods below rather than by hand.
type Activity struct {
	model.Model

	// ID is a version 7 UUID, so the identifiers of entries written in one
	// instant still sort in the order they were written.
	ID string `db:"id"`

	// TenantID is the customer the entry belongs to. It is written from the
	// Grant the entry was recorded with, never from anything a caller names.
	TenantID string `db:"tenant_id"`

	// LogName is the log the entry belongs to; one table holds as many logs
	// as an application keeps.
	LogName *string `db:"log_name"`

	// Description is what happened, in words, after its placeholders were
	// replaced.
	Description string `db:"description"`

	// SubjectType and SubjectID name what the entry is about.
	SubjectType *string `db:"subject_type"`
	SubjectID   *string `db:"subject_id"`

	// Event is the kind of change, for an entry about a record's change.
	Event *string `db:"event"`

	// CauserType and CauserID name who caused it; both are null for an entry
	// with no causer.
	CauserType *string `db:"causer_type"`
	CauserID   *string `db:"causer_id"`

	// AttributeChanges is the JSON of a record's change: what its attributes
	// became and what they were. See Changes.
	AttributeChanges *string `db:"attribute_changes"`

	// PropertiesJSON is the JSON of whatever the caller attached. See
	// Properties.
	PropertiesJSON *string `db:"properties"`

	// CreatedAt is when it happened, in UTC.
	CreatedAt time.Time `db:"created_at"`

	// UpdatedAt is when the entry was last written, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// activityTable is the table of Activity. Its query, Activities, is generated
// beside it by aru model:build, and is the one way to reach the rows.
//
// The logger stamps the two timestamps itself, so an entry can be dated as
// CreatedAt says rather than as the moment it is written.
var activityTable = model.NewTable(model.TableSpec{
	Name:         ActivityTable,
	New:          func() model.Entity { return new(Activity) },
	UniqueIDs:    true,
	NoTimestamps: true,
})

// LogValue keeps what identifies an entry, and leaves out what it carries: an
// entry's properties and changes are exactly the data somebody chose to keep
// out of the ordinary logs.
func (a Activity) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", a.ID), slog.String("log", text(a.LogName)), slog.String("event", text(a.Event)))
}

// Ref names one thing an entry is about or was caused by: its kind and its
// identifier within that kind. Neither is a Go type: this package never loads
// the row on the other side.
type Ref struct {
	Type string
	ID   string
}

// IsZero reports whether the reference names nothing.
func (r Ref) IsZero() bool { return r.Type == "" && r.ID == "" }

// Subject is what the entry is about, or the zero Ref.
func (a *Activity) Subject() Ref { return Ref{Type: text(a.SubjectType), ID: text(a.SubjectID)} }

// Causer is who caused the entry, or the zero Ref for an anonymous one.
func (a *Activity) Causer() Ref { return Ref{Type: text(a.CauserType), ID: text(a.CauserID)} }

// Changes is a record's change: Attributes is what its logged attributes
// became, and Old is what they were. A created or restored record carries only
// Attributes; a deleted one only Old.
type Changes struct {
	Attributes map[string]any `json:"attributes,omitempty"`
	Old        map[string]any `json:"old,omitempty"`
}

// IsEmpty reports whether the change carries nothing.
func (c Changes) IsEmpty() bool { return len(c.Attributes) == 0 && len(c.Old) == 0 }

// Changes returns the record's change the entry carries.
func (a *Activity) Changes() Changes {
	var changes Changes
	if a.AttributeChanges != nil {
		_ = json.Unmarshal([]byte(*a.AttributeChanges), &changes)
	}
	return changes
}

// SetChanges replaces the record's change the entry carries. An empty change
// is stored as null.
func (a *Activity) SetChanges(changes Changes) error {
	if changes.IsEmpty() {
		a.AttributeChanges = nil
		return nil
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	value := string(encoded)
	a.AttributeChanges = &value
	return nil
}

// Properties returns what the caller attached to the entry.
func (a *Activity) Properties() map[string]any {
	properties := map[string]any{}
	if a.PropertiesJSON != nil {
		_ = json.Unmarshal([]byte(*a.PropertiesJSON), &properties)
	}
	return properties
}

// SetProperties replaces what the entry carries. No property is stored as
// null.
func (a *Activity) SetProperties(properties map[string]any) error {
	if len(properties) == 0 {
		a.PropertiesJSON = nil
		return nil
	}
	encoded, err := json.Marshal(normalizeMap(properties))
	if err != nil {
		return err
	}
	value := string(encoded)
	a.PropertiesJSON = &value
	return nil
}

// SetProperty adds one property to what the entry carries.
func (a *Activity) SetProperty(key string, value any) error {
	properties := a.Properties()
	properties[key] = value
	return a.SetProperties(properties)
}

// GetProperty reads one property by its dot path -- "address.city" -- and
// answers fallback when the path leads nowhere.
func (a *Activity) GetProperty(path string, fallback any) any {
	if value, ok := lookup(a.Properties(), path); ok {
		return value
	}
	return fallback
}

// The errors this package answers with.
var (
	// ErrNotFound is returned when no entry matches, including when it exists
	// in another tenant. The two cases are deliberately indistinguishable.
	ErrNotFound = errors.New("activitylog: entry not found")

	// ErrNoGrant is returned when an entry is recorded with a Grant that names
	// no tenant: an entry belongs to somebody, and the Grant is where who comes
	// from.
	ErrNoGrant = errors.New("activitylog: an entry is recorded under a Grant that names its tenant")
)

// lookup reads a dot path through nested maps.
func lookup(values map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	if value, ok := values[path]; ok {
		return value, true
	}
	var current any = values
	for _, segment := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		if current, ok = node[segment]; !ok {
			return nil, false
		}
	}
	return current, true
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
