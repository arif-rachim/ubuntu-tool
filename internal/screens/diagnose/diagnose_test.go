package diagnose

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/diagnose"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/checklist"
)

type dummy struct{ nav.Screen }

func TestMenu(t *testing.T) {
	opened := ""
	m := New(diagnose.Env{}, func(id string) nav.Screen { opened = id; return &dummy{} }, map[string]string{"disk": "Disk & Storage"})
	view := ansi.Strip(m.View(100, 40))
	if !strings.Contains(view, "1. Cek kesehatan umum") || !strings.Contains(view, "No space left on device") {
		t.Errorf("%s", view)
	}
	_, cmd := m.Update(testutil.Key("5"))
	if opened != "network:inbound" || testutil.Run(cmd)[0].(nav.PushMsg).Screen == nil {
		t.Errorf("gejala web harus membuka wizard inbound, dapat %q", opened)
	}
	_, cmd = m.Update(testutil.Key("2"))
	c, ok := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*checklist.Model)
	if !ok || !c.Independent || c.Title() != "Disk penuh" || c.ModuleName("disk") != "modul Disk & Storage" {
		t.Errorf("gejala disk harus membuka checklist independen: %+v", c)
	}
}
