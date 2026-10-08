package activitylog

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/view"
)

// The screens of the log: the entries, filtered and paged, and one entry with
// what changed and what it carries -- each with what it is about and who
// caused it. The sources are embedded here and published into the
// application, which compiles them with its own views; until it does, the
// read routes answer JSON.
//
//go:embed resources/publish/*.kyse.go
var viewSources embed.FS

const (
	viewRoot     = "resources/publish"
	viewPrefix   = "resources/views/modules/activitylog"
	viewSuffix   = ".kyse.go"
	compiledRoot = "storage/framework/views"
)

// The names the screens are registered under once compiled.
const (
	ViewIndex = "modules.activitylog.index"
	ViewShow  = "modules.activitylog.show"
)

// PublishCommand is what an application runs to take the screens over.
const PublishCommand = "aru vendor:publish --apply"

// Row is one entry as a screen shows it.
type Row struct {
	ID, URL, When, Log, Event, Description string
	Subject, Causer                         string
}

// ChangeRow is one attribute of a record's change.
type ChangeRow struct{ Attribute, Old, New string }

// PropertyRow is one property of an entry.
type PropertyRow struct{ Key, Value string }

// IndexPageData is the log, one page of it.
type IndexPageData struct {
	view.Page
	Prefix, Log, Event, Search string
	Labels                     Labels
	Rows                       []Row
	PageNumber, Pages, Total   int
	PreviousURL, NextURL       string
}

// ShowPageData is one entry.
type ShowPageData struct {
	view.Page
	Prefix     string
	Labels     Labels
	Row        Row
	Changes    []ChangeRow
	Properties []PropertyRow
}

var (
	_ view.Layout = IndexPageData{}
	_ view.Layout = ShowPageData{}
)

// Labels are the words of the screens, in one locale.
type Labels struct{ words map[string]string }

// T is the word for key, or the key itself when the locale has none.
func (l Labels) T(key string) string {
	if word, ok := l.words[key]; ok {
		return word
	}
	return key
}

var catalog = map[string]map[string]string{
	"en": {
		"title": "Activity", "lead": "What happened, to what, and who did it.",
		"when": "When", "log": "Log", "event": "Event", "description": "Description", "subject": "Subject", "causer": "Caused by",
		"search": "Search", "filter": "Filter", "empty": "No activity yet", "previous": "Previous", "next": "Next",
		"changes": "What changed", "attribute": "Attribute", "old": "Before", "new": "After", "no_changes": "This entry carries no record change.",
		"properties": "Properties", "key": "Key", "value": "Value", "no_properties": "Nothing was attached to this entry.",
		"back": "Back", "system": "System", "page": "Page", "of": "of",
	},
	"pt-BR": {
		"title": "Atividade", "lead": "O que aconteceu, em que registro, e quem fez.",
		"when": "Quando", "log": "Log", "event": "Evento", "description": "Descrição", "subject": "Registro", "causer": "Causado por",
		"search": "Buscar", "filter": "Filtrar", "empty": "Nenhuma atividade ainda", "previous": "Anterior", "next": "Próxima",
		"changes": "O que mudou", "attribute": "Atributo", "old": "Antes", "new": "Depois", "no_changes": "Esta entrada não carrega a mudança de um registro.",
		"properties": "Propriedades", "key": "Chave", "value": "Valor", "no_properties": "Nada foi anexado a esta entrada.",
		"back": "Voltar", "system": "Sistema", "page": "Página", "of": "de",
	},
}

// LabelsFor are the words of a locale this package carries; English for any
// other.
func LabelsFor(locale string) Labels {
	if words, ok := catalog[locale]; ok {
		return Labels{words: words}
	}
	return Labels{words: catalog["en"]}
}

// Publishes declares the screens' sources for `aru vendor:publish`.
func (m *Module) Publishes() []foundation.Publication {
	return []foundation.Publication{{Tag: foundation.PublishView, Files: viewSources, From: viewRoot, To: viewPrefix}}
}

var _ foundation.Publishable = (*Module)(nil)

// ViewNames are the names the screens register once compiled.
func ViewNames() []string { return []string{ViewIndex, ViewShow} }

// ViewPackages are the compiled packages an application imports so the
// screens are linked.
func ViewPackages() []string { return []string{compiledRoot + "/modules/activitylog"} }

// missingViews are the screens the binary does not hold.
func missingViews() []string {
	registered := map[string]bool{}
	for _, name := range view.Registered() {
		registered[name] = true
	}
	var missing []string
	for _, name := range ViewNames() {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// checkViews answers why the screens cannot be drawn, or nil.
func checkViews() error {
	missing := missingViews()
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("activitylog: no view is registered as %s. Run `%s`, then `aru view:build`, then import <module path>/%s in bootstrap/app.go",
		strings.Join(missing, ", "), PublishCommand, ViewPackages()[0])
}

// embeddedViews lists the sources the archive holds; it is what Publishes
// hands over, read here so a test can say the archive is whole.
func embeddedViews() []string {
	var out []string
	_ = fs.WalkDir(viewSources, viewRoot, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, viewSuffix) {
			out = append(out, viewPrefix+strings.TrimPrefix(path, viewRoot))
		}
		return err
	})
	return out
}

// PublishedPaths are where the sources land in the application.
func PublishedPaths() []string { return embeddedViews() }
