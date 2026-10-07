package ui

import (
	"testing"
	"time"

	"lazy1c/internal/config"
)

// TestPausedClustersSurviveRestart: адреса из paused_clusters стартуют
// с enabled=false — и из конфига, и из добавленных через UI.
func TestPausedClustersSurviveRestart(t *testing.T) {
	cfg := &config.Config{Clusters: []config.Cluster{
		{Name: "a", Address: "a:1545"},
		{Name: "b", Address: "b:1545"},
	}}
	st := defaultSettings()
	st.Clusters = []ExtraCluster{{Name: "ui", Address: "ui:1545"}}
	st.Paused = []string{"a:1545", "ui:1545"}

	states := buildClusterStates(cfg, st, time.Second)
	byAddr := map[string]bool{}
	for _, s := range states {
		byAddr[s.address] = s.enabled
	}
	if byAddr["a:1545"] {
		t.Error("кластер a должен стартовать на паузе (paused_clusters)")
	}
	if !byAddr["b:1545"] {
		t.Error("кластер b не на паузе — должен опрашиваться")
	}
	if byAddr["ui:1545"] {
		t.Error("кластер, добавленный из UI, должен стартовать на паузе")
	}
}

// TestConsoleCategoryMMC: категория «консоли» прячет только SrvrConsole;
// толстый/тонкий клиент (Предприятие, любое имя) и веб — никогда.
func TestConsoleCategoryMMC(t *testing.T) {
	m := filterTestModel()
	m.settings = defaultSettings()
	m.settings.HideConsole = true

	cases := []struct {
		app, user string
		hidden    bool
	}{
		{"SrvrConsole", "Admin", true},   // сеанс MMC-консоли — прячем
		{"SrvrConsole", "", true},        // без имени — тоже консольный
		{"1CV8", "DefUser", false},       // Предприятие (толстый) с именем
		{"1CV8", "", false},              // безымянный толстый — тоже Предприятие (скрипт)
		{"1CV8C", "Admin", false},        // тонкий
		{"WebClient", "Admin", false},    // веб
	}
	for _, c := range cases {
		if got := m.sessionHidden(c.app, c.user); got != c.hidden {
			t.Errorf("sessionHidden(%q, %q) = %v, хочу %v", c.app, c.user, got, c.hidden)
		}
	}
}
