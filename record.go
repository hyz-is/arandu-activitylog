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
	Fresh(ctx context.Context, g security.Grant, with ...string) (model.Entity, error)
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

// DoesNotRecordEvents is implemented by a record that leaves some events out of
// the ones it records -- Spatie's static $doNotRecordEvents.
type DoesNotRecordEvents interface {
	ActivityEventsNotRecorded() []string
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

// LogOnly logs the named attributes, in place of any named before, as
// Spatie's logOnly replaces. "a.b" reads b on the relation a and is
// kept under the flat key "a.b"; "a->b" reads b inside the JSON column a and
// is kept nested.
func (o LogOptions) LogOnly(attributes ...string) LogOptions {
	o.logAttributes = slices.Clone(attributes)
	return o
}

// LogExcept never logs the named attributes.
func (o LogOptions) LogExcept(attributes ...string) LogOptions {
	o.logExceptAttributes = slices.Clone(attributes)
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
	o.dontLogIfAttributesChangedOnly = slices.Clone(attributes)
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
	o.attributeRawValues = slices.Clone(attributes)
	return o
}

// Save saves the record under g and logs its creation or its update, in one
// transaction: the row and the entry about it are written together or not at
// all. It answers what the record's own Save answers.
//
// What the attributes were is read from the row as stored before the write,
// and what they became is read back after it, both with the relations the
// options name -- Spatie's getRawOriginal and fresh(). Clearing deleted_at is a restore, and is not
// logged as an update, as Spatie's isRestoring.
func (l *Logger) Save(ctx context.Context, g security.Grant, record Record) (bool, error) {
	event := EventUpdated
	if !record.Exists() {
		event = EventCreated
	}
	options := optionsOf(record)
	logs := l.Enabled(ctx) && recordsEvent(record, event) && !recordDisabled(record)
	original := record.GetOriginal()
	if logs && !changedBeyond(record.GetDirty(), options.dontLogIfAttributesChangedOnly) {
		logs = false
	}
	if logs && event == EventUpdated && restoring(original, record.GetAttributes()) {
		logs = false
	}
	var saved bool
	err := data.Transaction(ctx, l.db, func(ctx context.Context) error {
		var before map[string]any
		if logs && event == EventUpdated {
			// What the row holds before this write, as stored -- the same
			// reading the after side gets, so a value is never reported as
			// changed by the precision it was kept in.
			attributes, array, err := snapshot(ctx, g, record, options)
			if err != nil {
				return err
			}
			before = l.logged(options, attributes, array)
		}
		var err error
		if saved, err = record.Save(ctx, g); err != nil || !saved || !logs {
			return err
		}
		attributes, array, err := snapshot(ctx, g, record, options)
		if err != nil {
			return err
		}
		after := l.logged(options, attributes, array)
		changes := Changes{Attributes: after}
		if event == EventUpdated {
			old := map[string]any{}
			for key := range after {
				old[key] = nil
			}
			for key, value := range before {
				old[key] = value
			}
			if options.logOnlyDirty {
				for key, value := range after {
					if sameValue(value, old[key]) {
						delete(after, key)
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
// attributes were, in one transaction. On a table with soft deletes the row
// stays, stamped, and the entry is the same.
func (l *Logger) Delete(ctx context.Context, g security.Grant, record Record) (bool, error) {
	return l.deleting(ctx, g, record, record.Delete)
}

// ForceDeletable is a record of a table with soft deletes that can be removed
// for good.
type ForceDeletable interface {
	Record
	ForceDelete(ctx context.Context, g security.Grant) (bool, error)
}

// ForceDelete removes the record for good under g and logs it as deleted, as
// Spatie logs a forceDelete.
func (l *Logger) ForceDelete(ctx context.Context, g security.Grant, record ForceDeletable) (bool, error) {
	return l.deleting(ctx, g, record, record.ForceDelete)
}

func (l *Logger) deleting(ctx context.Context, g security.Grant, record Record, remove func(context.Context, security.Grant) (bool, error)) (bool, error) {
	options := optionsOf(record)
	logs := l.Enabled(ctx) && recordsEvent(record, EventDeleted) && !recordDisabled(record)
	var deleted bool
	err := data.Transaction(ctx, l.db, func(ctx context.Context) error {
		var err error
		if deleted, err = remove(ctx, g); err != nil || !deleted || !logs {
			return err
		}
		attributes, array, err := snapshot(ctx, g, record, options)
		if err != nil {
			return err
		}
		return l.logChange(ctx, g, record, options, EventDeleted, Changes{Old: l.logged(options, attributes, array)})
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
		attributes, array, err := snapshot(ctx, g, record, options)
		if err != nil {
			return err
		}
		return l.logChange(ctx, g, record, options, EventRestored, Changes{Attributes: l.logged(options, attributes, array)})
	})
	return restored, err
}

// snapshot reads the record back as stored, with the relations its options
// name loaded; a row that is gone answers what the record holds.
func snapshot(ctx context.Context, g security.Grant, record Record, options LogOptions) (map[string]any, map[string]any, error) {
	fresh, err := record.Fresh(ctx, g, relationsOf(options)...)
	if err != nil {
		return nil, nil, err
	}
	if reread, ok := fresh.(Record); ok && reread != nil && fresh != nil {
		return reread.GetAttributes(), mergeArrays(reread.ToArray(), reread.GetAttributes()), nil
	}
	return record.GetAttributes(), mergeArrays(record.ToArray(), record.GetAttributes()), nil
}

// relationsOf are the relations a record's logged dot paths go through.
func relationsOf(options LogOptions) []string {
	var out []string
	for _, name := range options.logAttributes {
		if strings.Contains(name, ".") && !strings.Contains(name, "->") {
			relation, _, _ := strings.Cut(name, ".")
			if !slices.Contains(out, relation) {
				out = append(out, relation)
			}
		}
	}
	return out
}

// mergeArrays is array with every column of attributes it lacks: what a
// placeholder and a logged name can read.
func mergeArrays(array, attributes map[string]any) map[string]any {
	out := make(map[string]any, len(array)+len(attributes))
	for key, value := range attributes {
		out[key] = value
	}
	for key, value := range array {
		out[key] = value
	}
	return out
}

// restoring says whether a save only takes a row out of the trash.
func restoring(original, current map[string]any) bool {
	before, had := original["deleted_at"]
	after, has := current["deleted_at"]
	return had && has && normalizeValue(before) != nil && normalizeValue(after) == nil
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
	_, err := l.In(ctx, g, options.logName).Event(event).On(record).WithChanges(changes).Log(description)
	return err
}

// logged picks, from one snapshot of a record, the attributes its options
// log: LogOnly's names ("*" for all), minus LogExcept's and the configured
// DefaultExceptAttributes. A name the record does not hold is logged as null,
// as Spatie logs it. Relation paths ("a.b") are read from array, which carries
// the loaded relations, trying the relation's name as written, in snake case
// and in camel case; JSON paths ("a->b") from the column a, nested, null where
// the path leads nowhere.
func (l *Logger) logged(options LogOptions, attributes, array map[string]any) map[string]any {
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
			segments := strings.Split(path, "->")
			value, _ := jsonPath(attributes[column], segments)
			nest(out, append([]string{column}, segments...), value)
		case strings.Contains(name, "."):
			value, _ := relationPath(array, name)
			out[name] = value
		default:
			if value, ok := attributes[name]; ok {
				out[name] = value
			} else {
				out[name] = array[name]
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

// relationPath reads a dot path through loaded relations, matching each
// segment as written, in snake case or in camel case: a relation the row has
// loaded, or a column of the row the path has reached.
func relationPath(values map[string]any, path string) (any, bool) {
	var current any = values
	for _, segment := range strings.Split(path, ".") {
		next, found := step(current, segment)
		if !found {
			return nil, false
		}
		current = next
	}
	return current, true
}

type relationHolder interface {
	GetRelation(relation string) (any, bool)
}

// step reads one segment of a path from a map, a loaded relation or a row.
func step(current any, segment string) (any, bool) {
	for _, name := range []string{segment, snakeCase(segment), camelCase(segment)} {
		if holder, ok := current.(relationHolder); ok {
			if related, loaded := holder.GetRelation(name); loaded {
				return related, true
			}
		}
		if node, ok := asMap(current); ok {
			if value, ok := node[name]; ok {
				return value, true
			}
		}
	}
	return nil, false
}

// asMap reads a row as its attributes: a map as it is, a row by its ToArray or
// its attributes, anything else by the JSON it writes.
func asMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return v, true
	case interface{ ToArray() map[string]any }:
		return v.ToArray(), true
	case interface{ GetAttributes() map[string]any }:
		return v.GetAttributes(), true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(encoded, &out) != nil || out == nil {
		return nil, false
	}
	return out, true
}

func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func camelCase(name string) string {
	parts := strings.Split(name, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
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
	if excluded, ok := record.(DoesNotRecordEvents); ok && slices.Contains(excluded.ActivityEventsNotRecorded(), event) {
		return false
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

// RefOf is a record's kind and key, as an entry names it: ActivityType when
// the record says, its table's name otherwise. It is what a Filter takes to
// read the entries about a record or caused by one -- Spatie's forSubject and
// causedBy.
func RefOf(record Record) Ref {
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
