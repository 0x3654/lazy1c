package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

func filterTestModel() *model {
	cuuid := "c1"
	snap := &ras.Snapshot{Clusters: []*serializev1.ClusterInfo{{Uuid: cuuid, Name: "K"}},
		Infobases: map[string][]*serializev1.InfobaseSummaryInfo{cuuid: {
			{Uuid: "ib-erp", Name: "erp_demo"},
			{Uuid: "ib-trade", Name: "trade_opti"},
		}},
		Sessions: map[string][]*serializev1.SessionInfo{cuuid: {
			{Uuid: "s1", InfobaseId: "ib-erp", AppId: "1CV8C", UserName: "Вася", Host: "h"},
			{Uuid: "s2", InfobaseId: "ib-trade", AppId: "1CV8C", UserName: "Петя", Host: "h"},
			{Uuid: "s3", InfobaseId: "ib-erp", AppId: "RAS", UserName: "", Host: "srv"}, // служебный
		}},
	}
	m := &model{cfg: &config.Config{}}
	m.state = []clusterState{{id: "t", address: "a", cluster: snap.Clusters[0], snap: snap,
		enabled: true, updated: time.Now(), expanded: true, ibOpen: map[string]bool{"ib-erp": true, "ib-trade": true}}}
	m.width, m.height = 150, 24
	m.rebuild()
	return m
}

// TestFilter: «/» фильтрует базы и сеансы, esc сбрасывает.
func TestFilter(t *testing.T) {
	m := filterTestModel()
	m.filter = "торг" // не найдёт — имена латиницей
	m.filter = "trade"
	m.filterScope = 1 // базовый уровень
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if strings.Contains(v, "erp_demo") {
		t.Error("erp_demo должен скрыться под фильтром trade")
	}
	if !strings.Contains(v, "trade_opti") {
		t.Error("trade_opti должен остаться под фильтром")
	}
	// фильтр по пользователю раскрывает его базу, даже если имя базы не матчится
	m.filter = "Вася"
	m.filterScope = 2 // сеансовый уровень
	m.rebuild()
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "erp_demo") || !strings.Contains(v, "Вася") {
		t.Errorf("фильтр по пользователю не нашёл базу: %s", firstLines(v))
	}
	if strings.Contains(v, "trade_opti") {
		t.Error("чужая база должна скрыться")
	}
	m.filter = ""
	m.filterScope = 0
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.View().Content), "trade_opti") {
		t.Error("сброс фильтра не вернул базу")
	}
}

// TestHideIdle: z прячет только спящих; RAS управляется настройкой, не z.
func TestHideIdle(t *testing.T) {
	m := filterTestModel()
	snap := m.state[0].snap
	snap.Sessions["c1"] = append(snap.Sessions["c1"],
		&serializev1.SessionInfo{Uuid: "s4", InfobaseId: "ib-erp", AppId: "1CV8C",
			UserName: "Соня", Host: "h", Hibernate: true})
	m.settings = Settings{HideRAS: false} // RAS показываем — управляет только настройка
	m.cursor = 0
	m.rebuild()

	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x7A}) // z
	m = m2.(*model)
	if !m.settings.HideIdle {
		t.Fatal("z не включила скрытие")
	}
	v := ansi.Strip(m.View().Content)
	if strings.Contains(v, "Соня") {
		t.Error("спящий сеанс должен скрыться по z")
	}
	if !strings.Contains(v, "RAS") {
		t.Error("RAS не должен зависеть от z — только от настройки")
	}
	// счётчик кластера учитывает видимость: 4 сеанса, RAS виден, спящий скрыт → 3
	if !strings.Contains(v, "👤3") {
		t.Errorf("счётчик кластера не учитывает видимость: %s", firstLines(v))
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x7A}) // z — показать
	m = m2.(*model)
	if m.settings.HideIdle {
		t.Fatal("повторная z не выключила скрытие")
	}
}

// TestConfirmOnQuit: при confirm_on_quit=q подтверждение, y — выход.
func TestConfirmOnQuit(t *testing.T) {
	m := filterTestModel()
	m.cfg.ConfirmOnQuit = true
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x71}) // q
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("подтверждение выхода не появилось")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6E}) // n — остаёмся
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("n не отменило выход")
	}
}

func firstLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return strings.Join(lines, " ⏎ ")
}

// TestAppIDLabel: служебные идентификаторы приложений получают человеческие подписи.
func TestAppIDLabel(t *testing.T) {
	cases := map[string]string{
		"1CV8C":         "тонкий",
		"1CV8":          "толстый",
		"WebClient":     "веб",
		"Designer":      "конфигуратор",
		"JobScheduler":  "планировщик",
		"RAS":           "RAS",
		"BackgroundJob": "регл.задание",
		"SomethingElse": "SomethingElse",
	}
	for in, want := range cases {
		if got := appIDLabel(in); got != want {
			t.Errorf("appIDLabel(%q) = %q, хочу %q", in, got, want)
		}
	}
}

// TestNoSessionsPlaceholder: раскрытие базы без сеансов показывает заглушку.
func TestNoSessionsPlaceholder(t *testing.T) {
	m := zoomTestModel() // у базы B есть сеансы — уберём их
	m.state[0].snap.Sessions["c1"] = nil
	m.state[0].ibOpen["ib1"] = true // раскрыли
	m.cursor = 1
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "— нет сеансов —") {
		t.Fatalf("заглушка не показана: %s", firstLines(v))
	}
	// стрелка вправо на свёрнутой базе без сеансов тоже даёт отклик
	m.state[0].ibOpen["ib1"] = false
	m.rebuild()
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x6C}) // l — раскрыть
	m = m2.(*model)
	if !strings.Contains(ansi.Strip(m.View().Content), "— нет сеансов —") {
		t.Fatal("после l у пустой базы нет заглушки")
	}
}

// TestHideJobs: настройка прячет регламентные сеансы из дерева/счётчика
// кластера; бейдж базы ⚙ остаётся (как 👤 у спящих).
func TestHideJobs(t *testing.T) {
	m := filterTestModel()
	snap := m.state[0].snap
	snap.Sessions["c1"] = append(snap.Sessions["c1"],
		&serializev1.SessionInfo{Uuid: "s-job", InfobaseId: "ib-erp", AppId: "BackgroundJob",
			UserName: "фоновое", Host: "h"})
	m.settings = Settings{HideRAS: false}
	m.cursor = 0
	m.rebuild()

	m.settings.HideJobs = true
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if strings.Contains(v, "фоновое") {
		t.Error("регламентный сеанс должен скрыться из дерева")
	}
	if !strings.Contains(v, "NO РЗ") {
		t.Error("нет бейджа NO РЗ в шапке")
	}
	if strings.Contains(v, "⚙2") {
		t.Error("счётчик кластера не должен считать скрытые регламентные (⚙1)")
	}
	if !strings.Contains(v, "⚙1") {
		t.Errorf("бейдж базы ⚙ должен остаться: %s", firstLines(v))
	}

	m.settings.HideJobs = false
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.View().Content), "фоновое") {
		t.Error("выключение настройки должно вернуть регламентный сеанс")
	}
}
