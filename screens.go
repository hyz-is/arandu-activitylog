package activitylog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/view"
)

// indexPage draws one numbered page of the log.
func (m *Module) indexPage(ctx *fhttp.Context, filter Filter) error {
	page, _ := strconv.Atoi(ctx.Query("page"))
	result, err := m.svc.Paginate(ctx.Ctx(), m.subject(ctx.Request), filter, page, m.cfg.PageSize)
	if err != nil {
		return m.answer(ctx, err)
	}
	labels := LabelsFor(m.cfg.Locale)
	data := IndexPageData{Page: m.page(ctx, labels.T("title")), Prefix: m.cfg.Prefix, Labels: labels,
		Log: ctx.Query("log"), Event: filter.Event, Search: filter.Search,
		PageNumber: result.Page, Pages: result.Pages, Total: result.Total}
	for _, entry := range result.Items {
		data.Rows = append(data.Rows, m.row(ctx, labels, entry))
	}
	query := ctx.Request.URL.Query()
	if result.Page > 1 {
		query.Set("page", strconv.Itoa(result.Page-1))
		data.PreviousURL = m.cfg.Prefix + "?" + query.Encode()
	}
	if result.Page < result.Pages {
		query.Set("page", strconv.Itoa(result.Page+1))
		data.NextURL = m.cfg.Prefix + "?" + query.Encode()
	}
	return ctx.View(ViewIndex, data)
}

// showPage draws one entry, with what changed and what it carries.
func (m *Module) showPage(ctx *fhttp.Context, entry *Activity) error {
	labels := LabelsFor(m.cfg.Locale)
	data := ShowPageData{Page: m.page(ctx, labels.T("title")), Prefix: m.cfg.Prefix, Labels: labels, Row: m.row(ctx, labels, entry)}
	changes := entry.Changes()
	keys := map[string]bool{}
	for key := range changes.Attributes {
		keys[key] = true
	}
	for key := range changes.Old {
		keys[key] = true
	}
	for _, key := range sorted(keys) {
		data.Changes = append(data.Changes, ChangeRow{Attribute: key, Old: shown(changes.Old, key), New: shown(changes.Attributes, key)})
	}
	properties := entry.Properties()
	names := map[string]bool{}
	for key := range properties {
		names[key] = true
	}
	for _, key := range sorted(names) {
		data.Properties = append(data.Properties, PropertyRow{Key: key, Value: shown(properties, key)})
	}
	return ctx.View(ViewShow, data)
}

// page is the chrome of a screen, drawn by view.New: the title, the path, the
// CSRF token and the application name the framework put on the request, and
// the navigation of the routes the application registered.
func (m *Module) page(ctx *fhttp.Context, title string) view.Page {
	return view.New(ctx, title)
}

// row is one entry as the screens show it.
func (m *Module) row(ctx *fhttp.Context, labels Labels, entry *Activity) Row {
	return Row{
		ID: entry.ID, URL: m.cfg.Prefix + "/" + url.PathEscape(entry.ID),
		When: entry.CreatedAt.UTC().Format(time.DateTime), Log: dash(text(entry.LogName)), Event: dash(text(entry.Event)),
		Description: entry.Description, Subject: m.named(ctx, entry.Subject(), "—"), Causer: m.named(ctx, entry.Causer(), labels.T("system")),
	}
}

// named is how a screen names a subject or a causer.
func (m *Module) named(ctx *fhttp.Context, ref Ref, nobody string) string {
	if ref.IsZero() {
		return nobody
	}
	if m.cfg.Name != nil {
		if name := m.cfg.Name(ctx.Ctx(), ref); name != "" {
			return name
		}
	}
	id := ref.ID
	if before, _, found := strings.Cut(id, "-"); found && len(before) >= 8 {
		id = before
	}
	return strings.TrimSpace(ref.Type + " " + id)
}

func shown(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return "—"
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func dash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func sorted(keys map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
