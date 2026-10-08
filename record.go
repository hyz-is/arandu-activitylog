package activitylog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/model"
)

// Record is a row of the application's that an entry can be about or caused
// by: any struct embedding the Hesape Model, whose methods it promotes.
type Record interface {
	model.Entity
	Exists() bool
	GetKey() any
	Table() *model.Table
	GetAttributes() map[string]any
	GetOriginal() map[string]any
	GetDirty() map[string]any
	ToArray() map[string]any
	Save(ctx context.Context, g security.Grant) (bool, error)
	Delete(ctx context.Context, g security.Grant) (bool, error)
}

// Typed is implemented by a record whose kind is not its table's name -- the
// morph name Spatie reads from getMorphClass.
type Typed interface {
	ActivityType() string
}

// LogsActivity is implemented by a record that says how its changes are
// logged -- Spatie's getActivitylogOptions. A record that does not implement
// it logs its events with no attribute.
type LogsActivity interface {
	ActivityLogOptions() LogOptions
}

// RecordsEvents is implemented by a record that logs other events than
// DefaultEvents -- Spatie's static $recordEvents.
type RecordsEvents interface {
	ActivityEvents() []string
}

// BeforeActivityLogged is implemented by a record that adjusts every entry
// about it, manual or automatic, right before it is written.
type BeforeActivityLogged interface {
	BeforeActivityLogged(a *Activity, event string)
}

// LoggingDisabled is implemented by a record that can turn its own logging
// off -- Spatie's $model->disableLogging().
type LoggingDisabled interface {
	ActivityLoggingDisabled() bool
}

// LogOptions say which of a record's attributes are logged and when: Spatie's
// LogOptions, as a value built by chaining.
type LogOptions struct {
	logName                        string
	logEmptyChanges                bool
	logOnlyDirty                   bool
	logAttributes                  []string
	logExceptAttributes            []string
	dontLogIfAttributesChangedOnly []string
	attributeRawValues             []string
	descriptionForEvent            func(event string) string
}

// DefaultLogOptions logs the events with no attribute, and logs an entry even
// when its change is empty.
func DefaultLogOptions() LogOptions { return LogOptions{logEmptyChanges: true} }

// LogAll logs every attribute.
func (o LogOptions) LogAll() LogOptions { return o.LogOnly("*") }

// LogOnly logs the named attributes. "a.b" reads b on the relation a and is
// kept under the flat key "a.b"; "a->b" reads b inside the JSON column a and
// is kept nested.
func (o LogOptions) LogOnly(attributes ...string) LogOptions {
	o.logAttributes = append(slices.Clone(o.logAttributes), attributes...)
	return o
}

// LogExcept never logs the named attributes.
func (o LogOptions) LogExcept(attributes ...string) LogOptions {
	o.logExceptAttributes = append(slices.Clone(o.logExceptAttributes), attributes...)
	return o
}

// LogOnlyDirty keeps, in an update, only the attributes whose value changed.
func (o LogOptions) LogOnlyDirty() LogOptions {
	o.logOnlyDirty = true
	return o
}

// DontLogIfAttributesChangedOnly skips an update that changed nothing but the
// named attributes -- typically updated_at.
func (o LogOptions) DontLogIfAttributesChangedOnly(attributes ...string) LogOptions {
	o.dontLogIfAttributesChangedOnly = append(slices.Clone(o.dontLogIfAttributesChangedOnly), attributes...)
	return o
}

// DontLogEmptyChanges skips an entry whose change carries nothing.
func (o LogOptions) DontLogEmptyChanges() LogOptions {
	o.logEmptyChanges = false
	return o
}

// LogEmptyChanges logs an entry even when its change carries nothing.
func (o LogOptions) LogEmptyChanges() LogOptions {
	o.logEmptyChanges = true
	return o
}

// UseLogName puts the record's entries in the named log.
func (o LogOptions) UseLogName(logName string) LogOptions {
	o.logName = logName
	return o
}

// SetDescriptionForEvent describes the record's entries; the default is the
// event's name. An empty description skips the entry.
func (o LogOptions) SetDescriptionForEvent(fn func(event string) string) LogOptions {
	o.descriptionForEvent = fn
	return o
}

// UseAttributeRawValues keeps the named attributes as the record holds them,
// without the normalization every other value gets.
func (o LogOptions) UseAttributeRawValues(attributes ...string) LogOptions {
	o.attributeRawValues = append(slices.Clone(o.attributeRawValues), attributes...)
	return o
}

// Save saves the record under g and logs its creation or its update, in one
// transaction: the row and the entry about it are written together or not at
// all. It answers what the record's own Save answers.
func (l *Logger) Save(ctx context.Context, g security.Grant, record Record) (bool, error) {
	event := EventUpdated
	if !record.Exists() {
		event = EventCreated
	}
	options := optionsOf(record)
	logs := l.Enabled(ctx) && recordsEvent(record, event) && !recordDisabled(record)
	var original map[string]any
	if logs && event == EventUpdated {
		dirty := record.GetDirty()
		if !changedBeyond(dirty, options.dontLogIfAttributesChangedOnly) {
			logs = false
		}
		original = record.GetOriginal()
	}
	var saved bool
	err := data.Transaction(ctx, l.db, func(ctx context.Context) error {
		var err error
		if saved, err = record.Save(ctx, g); err != nil || !saved || !logs {
			return err
		}
		attributes := l.logged(record, options, record.GetAttributes(), record.ToArray())
		changes := Changes{Attributes: attributes}
		if event == EventUpdated {
			old := map[string]any{}
			for key := range attributes {
				old[key] = nil
			}
			for key, value := range l.logged(record, options, original, original) {
				old[key] = value
			}
			if options.logOnlyDirty {
				for key, value := range attributes {
					if sameValue(value, old[key]) {
						delete(attributes, key)
						delete(old, key)
					}
				}
			}
			changes.Old = old
		}
		return l.logChange(ctx, g, record, options, event, changes)
	})
	return saved, err
}

// Delete deletes the record under g and logs it, with what its logged
// attributes were, in one transaction.
func (l *Logger) Delete(ctx context.Context, g security.Grant, record Record) (bool, error) {
	options := optionsOf(record)
	logs := l.Enabled(ctx) && recordsEvent(record, EventDeleted) && !recordDisabled(record)
	old := l.logged(record, options, record.GetAttributes(), record.ToArray())
	var deleted bool
	err := data.Transaction(ctx, l.db, func(ctx context.Context) error {
		var err error
		if deleted, err = record.Delete(ctx, g); err != nil || !deleted || !logs {
			return err
		}
		return l.logChange(ctx, g, record, options, EventDeleted, Changes{Old: old})
	})
	return deleted, err
}

// Restorable is a record of a table with soft deletes.
type Restorable interface {
	Record
	Restore(ctx context.Context, g security.Grant) (bool, error)
}

// Restore restores a soft-deleted record under g and logs it, in one
// transaction.
func (l *Logger) Restore(ctx context.Context, g security.Grant, record Restorable) (bool, error) {
	options := optionsOf(record)
	logs := l.Enabled(ctx) && recordsEvent(record, EventRestored) && !recordDisabled(record)
	var restored bool
	err := data.Transaction(ctx, l.db, func(ctx context.Context) error {
		var err error
		if restored, err = record.Restore(ctx, g); err != nil || !restored || !logs {
			return err
		}
		attributes := l.logged(record, options, record.GetAttributes(), record.ToArray())
		return l.logChange(ctx, g, record, options, EventRestored, Changes{Attributes: attributes})
	})
	return restored, err
}

// logChange writes the entry about a record's change, as Spatie's
// LogsActivity does once the change is computed.
func (l *Logger) logChange(ctx context.Context, g security.Grant, record Record, options LogOptions, event string, changes Changes) error {
	description := event
	if options.descriptionForEvent != nil {
		description = options.descriptionForEvent(event)
	}
	if description == "" {
		return nil
	}
	if !options.logEmptyChanges && changes.IsEmpty() {
		return nil
	}
	_, err := l.Activity(ctx, g).InLog(options.logName).Event(event).On(record).WithChanges(changes).Log(description)
	return err
}

// logged picks, from one snapshot of a record, the attributes its options
// log: LogOnly's names ("*" for all), minus LogExcept's and the configured
// DefaultExceptAttributes. Relation paths ("a.b") are read from array, which
// carries the loaded relations; JSON paths ("a->b") from the column a.
func (l *Logger) logged(record Record, options LogOptions, attributes, array map[string]any) map[string]any {
	out := map[string]any{}
	except := append(slices.Clone(options.logExceptAttributes), l.cfg.DefaultExceptAttributes...)
	raw := options.attributeRawValues
	for _, name := range options.logAttributes {
		switch {
		case name == "*":
			for key, value := range attributes {
				out[key] = value
			}
		case strings.Contains(name, "->"):
			column, path, _ := strings.Cut(name, "->")
			value, ok := jsonPath(attributes[column], strings.Split(path, "->"))
			if ok {
				nest(out, append([]string{column}, strings.Split(path, "->")...), value)
			}
		case strings.Contains(name, "."):
			value, _ := lookup(array, name)
			out[name] = value
		default:
			if value, ok := attributes[name]; ok {
				out[name] = value
			}
		}
	}
	for _, name := range except {
		delete(out, name)
	}
	for key, value := range out {
		if !slices.Contains(raw, key) {
			out[key] = normalizeValue(value)
		}
	}
	return out
}

// jsonPath reads a path inside a JSON column, held as text or as a map.
func jsonPath(value any, path []string) (any, bool) {
	var node any
	switch v := normalizeValue(value).(type) {
	case string:
		if json.Unmarshal([]byte(v), &node) != nil {
			return nil, false
		}
	default:
		node = v
	}
	for _, segment := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		if node, ok = m[segment]; !ok {
			return nil, false
		}
	}
	return node, true
}

// nest writes value at path inside out, making the maps on the way.
func nest(out map[string]any, path []string, value any) {
	current := out
	for _, segment := range path[:len(path)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	current[path[len(path)-1]] = value
}

func optionsOf(record Record) LogOptions {
	if logs, ok := record.(LogsActivity); ok {
		return logs.ActivityLogOptions()
	}
	return DefaultLogOptions()
}

func recordsEvent(record Record, event string) bool {
	events := DefaultEvents
	if custom, ok := record.(RecordsEvents); ok {
		events = custom.ActivityEvents()
	}
	return slices.Contains(events, event)
}

func recordDisabled(record Record) bool {
	disabled, ok := record.(LoggingDisabled)
	return ok && disabled.ActivityLoggingDisabled()
}

// changedBeyond says whether dirty holds a key outside ignored.
func changedBeyond(dirty map[string]any, ignored []string) bool {
	for key := range dirty {
		if !slices.Contains(ignored, key) {
			return true
		}
	}
	return false
}

// refOf is a record's kind and key.
func refOf(record Record) Ref {
	kind := ""
	if typed, ok := record.(Typed); ok {
		kind = typed.ActivityType()
	} else if table := record.Table(); table != nil {
		kind = table.Name()
	}
	key := record.GetKey()
	if key == nil {
		return Ref{Type: kind}
	}
	return Ref{Type: kind, ID: fmt.Sprint(key)}
}
