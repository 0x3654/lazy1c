package ui

import (
	"testing"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// TestAllVisibilityFilters — матрица: сеанс (app × имя × спящий) × настройки →
// виден/скрыт. Покрывает все категории: RAS, консоль MMC, конфигуратор,
// регл. задания, спящие.
func TestAllVisibilityFilters(t *testing.T) {
	sess := func(app, user string, idle bool) *serializev1.SessionInfo {
		return &serializev1.SessionInfo{AppId: app, UserName: user, Host: "h", Hibernate: idle}
	}
	cases := []struct {
		name                               string
		app, user                          string
		idle                               bool
		hideRAS, hideMMC, hideConf, hideJobs, hideIdle bool
		visible                            bool
	}{
		{"RAS-сеанс скрыт галкой", "RAS", "", false, true, false, false, false, false, false},
		{"RAS-сеанс виден без галки", "RAS", "", false, false, false, false, false, false, true},
		{"консоль кластера (MMC) скрыта", "SrvrConsole", "Admin", false, false, true, false, false, false, false},
		{"консоль безымянная скрыта", "SrvrConsole", "", false, false, true, false, false, false, false},
		{"консоль видна без галки", "SrvrConsole", "Admin", false, false, false, false, false, false, true},
		{"толстый с именем — всегда предприятие", "1CV8", "DefUser", false, false, true, false, false, false, true},
		{"толстый безымянный — тоже предприятие", "1CV8", "", false, false, true, false, false, false, true},
		{"тонкий не трогается консольной галкой", "1CV8C", "Admin", false, false, true, false, false, false, true},
		{"веб не трогается", "WebClient", "u", false, false, true, false, false, false, true},
		{"конфигуратор скрыт галкой", "Designer", "Vasya", false, false, false, true, false, false, false},
		{"конфигуратор виден без галки", "Designer", "Vasya", false, false, false, false, false, false, true},
		{"регл. задание скрыто", "BackgroundJob", "", false, false, false, false, true, false, false},
		{"фоновое (8.2 стиль) скрыто", "1CV8 фоновое задание", "", false, false, false, false, true, false, false},
		{"спящий скрыт галкой", "1CV8C", "u", true, false, false, false, false, true, false},
		{"спящий виден без галки", "1CV8C", "u", true, false, false, false, false, false, true},
		{"RAS-галка не трогает предприятие", "1CV8C", "u", false, true, false, false, false, false, true},
	}
	for _, c := range cases {
		m := filterTestModel()
		m.settings = defaultSettings()
		m.settings.HideRAS, m.settings.HideConsole = c.hideRAS, c.hideMMC
		m.settings.HideDesigner, m.settings.HideJobs, m.settings.HideIdle = c.hideConf, c.hideJobs, c.hideIdle
		s := sess(c.app, c.user, c.idle)
		if got := m.sessionShown(s); got != c.visible {
			t.Errorf("%s: sessionShown = %v, хочу %v", c.name, got, c.visible)
		}
	}
}

// TestCountersRespectFilters: 👤/⚙ у базы считают только видимых; кластерная
// строка — сеансы всех баз с теми же правилами.
func TestCountersRespectFilters(t *testing.T) {
	m := filterTestModel()
	m.settings = defaultSettings()
	m.settings.HideJobs = true
	sessions := []*serializev1.SessionInfo{
		{InfobaseId: "ib-erp", AppId: "1CV8C", UserName: "V"},
		{InfobaseId: "ib-erp", AppId: "Designer", UserName: "D"},
		{InfobaseId: "ib-erp", AppId: "BackgroundJob"},
		{InfobaseId: "ib-erp", AppId: "RAS"},
	}
	u, j := m.countUsersJobs(sessions, "ib-erp")
	if u != 2 || j != 0 {
		t.Errorf("👤=%d ⚙=%d, хочу 2/0 (РЗ скрыт галкой, RAS не в счётчиках никогда)", u, j)
	}
	m.settings.HideJobs = false
	u, j = m.countUsersJobs(sessions, "ib-erp")
	if u != 2 || j != 1 {
		t.Errorf("без галки РЗ: 👤=%d ⚙=%d, хочу 2/1", u, j)
	}
}

// TestTextFilterScopes: контекстный поиск режет по уровню курсора —
// базы фильтруются по имени, сеансы по атрибутам.
func TestTextFilterScopes(t *testing.T) {
	m := filterTestModel()
	m.settings = defaultSettings()
	m.filter, m.filterScope = "erp", 1 // курсор был на базе → ищем базы
	if !m.filterBases(&serializev1.InfobaseSummaryInfo{Name: "erp_demo"}) {
		t.Error("база erp_demo должна проходить фильтр erp")
	}
	if m.filterBases(&serializev1.InfobaseSummaryInfo{Name: "trade_opti"}) {
		t.Error("trade_opti не должна проходить фильтр erp")
	}
	m.filter, m.filterScope = "вас", 2 // сеансы
	if !m.filterSessions(&serializev1.SessionInfo{UserName: "Вася"}) {
		t.Error("сеанс Васи должен проходить фильтр vasya (без регистра)")
	}
	if m.filterSessions(&serializev1.SessionInfo{UserName: "Петя"}) {
		t.Error("Петя не проходит")
	}
}
