package feature_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	hhttp "github.com/arandu-io/hesape/http"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// An application that asks for the screens and has not published and compiled
// them is refused at boot, with the command that fixes it; one that does not
// ask boots and answers JSON.
func TestTheScreensAreRefusedAtBootUntilTheyAreCompiled(t *testing.T) {
	db := openDatabase(t)
	sessions := security.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, security.NewMemoryBackend())
	screens, err := activitylog.New(activitylog.Config{Tenant: "acme", Screens: true}, db, sessions)
	if err != nil {
		t.Fatal(err)
	}
	err = screens.Boot(context.Background())
	if err == nil || !strings.Contains(err.Error(), activitylog.PublishCommand) || !strings.Contains(err.Error(), activitylog.ViewIndex) {
		t.Errorf("boot without the views = %v", err)
	}
	plain, _ := activitylog.New(activitylog.Config{Tenant: "acme"}, db, sessions)
	if err := plain.Boot(context.Background()); err != nil {
		t.Errorf("a module without screens refused to boot: %v", err)
	}

	published := screens.Publishes()
	if len(published) != 1 || len(activitylog.PublishedPaths()) != 2 {
		t.Errorf("published = %+v, paths %v", published, activitylog.PublishedPaths())
	}
	for _, path := range activitylog.PublishedPaths() {
		if !strings.HasPrefix(path, "resources/views/modules/activitylog/") {
			t.Errorf("%s would land outside the module's view directory", path)
		}
	}
	for _, locale := range []string{"en", "pt-BR"} {
		if labels := activitylog.LabelsFor(locale); labels.T("title") == "title" || labels.T("causer") == "causer" {
			t.Errorf("%s has no words for the screens", locale)
		}
	}
	if activitylog.LabelsFor("pt-BR").T("title") != "Atividade" || activitylog.LabelsFor("xx").T("title") != "Activity" {
		t.Error("the locales are not the ones carried")
	}
}

// A screen carries the application name the framework puts on the request,
// and the CSRF token beside it, as every first-party module's screen does: a
// module's page drawn in the application's layout shows the application's
// brand without being handed it.
func TestTheScreensCarryTheApplicationNameOnTheRequest(t *testing.T) {
	db := openDatabase(t)
	sessions := security.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, security.NewMemoryBackend())
	module, err := activitylog.New(activitylog.Config{Tenant: "acme", Screens: true, Policy: openPolicy{}}, db, sessions)
	if err != nil {
		t.Fatal(err)
	}
	router := fhttp.NewRouter()
	module.Routes(router.ForModule(module.Name()))

	request := httptest.NewRequest(http.MethodGet, activitylog.DefaultPrefix, nil)
	request.Header.Set("Accept", hhttp.ViewDataMediaType)
	ctx := hhttp.WithAppName(request.Context(), "Acme Console")
	ctx = hhttp.WithCSRFToken(ctx, "token-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request.WithContext(ctx))
	if response.Code != http.StatusOK {
		t.Fatalf("the log answered %d: %s", response.Code, response.Body.String())
	}

	var answer struct {
		View string `json:"view"`
		Data struct {
			Title   string
			AppName string
			Token   string
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the view data does not decode: %v: %s", err, response.Body.String())
	}
	if answer.View != activitylog.ViewIndex {
		t.Errorf("the log drew %q", answer.View)
	}
	if answer.Data.AppName != "Acme Console" {
		t.Errorf("the log drew the brand %q, not the application name on the request", answer.Data.AppName)
	}
	if answer.Data.Token != "token-1" || answer.Data.Title != "Activity" {
		t.Errorf("the log lost its title or token: %+v", answer.Data)
	}
}
