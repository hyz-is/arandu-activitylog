package feature_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"

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
