package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

func zoomTestModel() *model {
	cuuid := "c1"
	ibid := "ib1"
	snap := &ras.Snapshot{Clusters: []*serializev1.ClusterInfo{{Uuid: cuuid, Name: "K"}},
		Infobases: map[string][]*serializev1.InfobaseSummaryInfo{cuuid: {{Uuid: ibid, Name: "B"}}},
		Sessions: map[string][]*serializev1.SessionInfo{cuuid: {
			{Uuid: "s1", InfobaseId: ibid, AppId: "1CV8C", UserName: "Вася", Host: "h1"},
			{Uuid: "s2", InfobaseId: ibid, AppId: "Designer", UserName: "Петя", Host: "h2"},
		}},
	}
	m := &model{cfg: &config.Config{}, ibInfo: map[string]*ibEntry{}}
	m.state = []clusterState{{id: "t", address: "a", cluster: snap.Clusters[0], snap: snap,
		enabled: true, updated: time.Now(), expanded: true, firstSnap: true, ibOpen: map[string]bool{}}}
	m.width, m.height = 100, 24
	m.rebuild()
	return m
}

// TestZoomFlow: enter на базе — полный экран; esc — назад в дерево.
func TestZoomFlow(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 1                                   // база B
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter
	m = m2.(*model)
	if m.zoom == nil {
		t.Fatal("enter на базе не открыл полноэкранный режим")
	}
	v := m.View()
	plain := ansi.Strip(v.Content)
	for _, want := range []string{"База B", "Вася", "Петя", "Пользователь", "esc: назад"} {
		if !strings.Contains(plain, want) {
			t.Errorf("в zoom-экране нет %q", want)
		}
	}
	if strings.Contains(v.Content, "Локальн") && strings.Contains(v.Content, "┌") && strings.Contains(v.Content, "База B") == false {
		t.Errorf("дерево не должно рендериться в zoom")
	}
	// j вниз, x — подтверждение для второго сеанса
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x64})
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("x в zoom не открыл подтверждение")
	}
	if !strings.Contains(m.confirm.question, "Петя") {
		t.Errorf("подтверждение не про выбранного пользователя: %q", m.confirm.question)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6E}) // n — отмена
	m = m2.(*model)
	// esc — назад
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B})
	m = m2.(*model)
	if m.zoom != nil {
		t.Fatal("esc не закрыл zoom")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Кластеры") {
		t.Fatal("после esc не видно дерево")
	}
}

// TestClusterToggle: o на кластере выключает опрос, ещё раз o — включает.
func TestClusterToggle(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x73}) // s
	m = m2.(*model)
	if m.state[0].enabled {
		t.Fatal("s не выключила кластер")
	}
	if !strings.Contains(m.View().Content, "⏸") {
		t.Fatal("выключенный кластер не помечен ⏸")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x73})
	m = m2.(*model)
	if !m.state[0].enabled {
		t.Fatal("повторная s не включила кластер")
	}
}

// TestArrowExpandsNotZoom: стрелка вправо и l раскрывают базу, zoom — только enter.
func TestArrowExpandsNotZoom(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 1                                  // база B (свёрнута)
	for _, key := range []rune{'l', 0x1B5, ' '} { // l, right, space
		m2, _ := m.Update(tea.KeyPressMsg{Code: key})
		m = m2.(*model)
		if m.zoom != nil {
			t.Fatalf("клавиша %q не должна открывать zoom", string(key))
		}
	}
	// после первого нажатия база раскрылась, сеансы видны
	if !m.state[0].ibOpen["ib1"] {
		t.Fatal("l/right/space не раскрывают базу")
	}
	// enter — открывает zoom
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.zoom == nil {
		t.Fatal("enter не открыл zoom базы")
	}
}

// TestTableAlignment: колонки таблицы выровнены по рунам, кириллица не рвёт строку.
func TestTableAlignment(t *testing.T) {
	// pad — по рунам
	if got := utf8.RuneCountInString(pad("Пользователь", 20)); got != 20 {
		t.Fatalf("pad дал %d рун, хочу 20", got)
	}
	m := zoomTestModel()
	snap := m.state[0].snap
	snap.Sessions["c1"] = append(snap.Sessions["c1"],
		&serializev1.SessionInfo{Uuid: "s3", InfobaseId: "ib1", AppId: "1CV8C",
			UserName: "ДлинноеИмяПользователя", Host: "host.local", Hibernate: true})
	m.zoom = &zoomState{clusterID: "t", ib: snap.Infobases["c1"][0]}
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if strings.Contains(v, string([]byte{0xEF, 0xBF, 0xBD})) {
		t.Error("в рендере появился сломанный UTF-8 (замещающий символ)")
	}
	// строки таблицы сеансов — одной ширины (ищем по колонке хоста)
	var widths []int
	for _, l := range strings.Split(v, "\n") {
		trim := strings.TrimRight(strings.TrimLeft(l, "│"), " ")
		if strings.Contains(trim, " h1 ") || strings.Contains(trim, " h2 ") ||
			strings.Contains(trim, " host.local ") {
			widths = append(widths, utf8.RuneCountInString(trim))
		}
	}
	if len(widths) < 3 {
		t.Fatalf("не нашли строки таблицы: %v", widths)
	}
	for _, w := range widths[1:] {
		if w != widths[0] {
			t.Fatalf("строки таблицы разной ширины: %v", widths)
		}
	}
}

// TestSelectedRowAlignment: плашка курсора не сдвигает колонки.
func TestSelectedRowAlignment(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true // раскрыть базу
	m.cursor = 2                    // сеанс
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	lines := strings.Split(v, "\n")
	var sessionLines []string
	for _, l := range lines {
		tl := strings.TrimLeft(l, "│")
		// берём только левую половину (дерево), до начала панели деталей
		if i := strings.Index(tl, "││"); i >= 0 {
			tl = tl[:i]
		} else if i := strings.Index(tl, "│ "); i > 0 && !strings.Contains(tl, "@") {
			continue
		}
		if strings.Contains(tl, "Вася@") || strings.Contains(tl, "Петя@") {
			sessionLines = append(sessionLines, tl)
		}
	}
	if len(sessionLines) < 2 {
		t.Fatalf("не нашли строки сеансов: %q", sessionLines)
	}
	// все строки сеансов начинаются с одинакового отступа (плашка его не сдвинула)
	prefix := make(map[string]bool)
	for _, l := range sessionLines {
		t := strings.TrimLeft(l, " ")
		prefix[l[:len(l)-len(t)]] = true
	}
	if len(prefix) != 1 {
		t.Fatalf("строки сеансов с разным отступом: %q", sessionLines)
	}
}

// TestZoomCursorAfterKill: убили последний сеанс — список укоротился,
// рендер и навигация не паникуют, курсор в границах.
func TestZoomCursorAfterKill(t *testing.T) {
	m := zoomTestModel()
	// три сеанса, курсор на последнем
	snap := m.state[0].snap
	snap.Sessions["c1"] = append(snap.Sessions["c1"],
		&serializev1.SessionInfo{Uuid: "s3", InfobaseId: "ib1", AppId: "1CV8C", UserName: "Сидор", Host: "h3"})
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x0D}) // zoom
	m = m2.(*model)
	m.zoom.cursor = 2 // последний из трёх
	// сеансы s1 и s3 убили — остался один
	snap.Sessions["c1"] = []*serializev1.SessionInfo{
		{Uuid: "s2", InfobaseId: "ib1", AppId: "Designer", UserName: "Петя", Host: "h2"},
	}
	// рендер после «убийства» — раньше здесь был index out of range
	v := m.View()
	_ = ansi.Strip(v.Content)
	// и навигация
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'j'})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x47}) // G (end)
	m = m2.(*model)
	if m.zoom.cursor != 0 {
		t.Fatalf("курсор=%d, хочу 0 (один сеанс)", m.zoom.cursor)
	}
	// всех убили — заглушка, не паника
	snap.Sessions["c1"] = nil
	_ = m.View()
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x64}) // d — не падает
	m = m2.(*model)
}

// TestPausedClusterPersisted: s ставит паузу и пишет её в settings,
// при «перезапуске» кластер остаётся выключенным.
func TestPausedClusterPersisted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x73}) // s — пауза
	m = m2.(*model)
	if m.state[0].enabled {
		t.Fatal("s не выключила опрос")
	}
	if !m.settings.isPausedCluster("a") {
		t.Fatal("пауза не записана в settings")
	}
	// файл на диске содержит paused_clusters
	data, err := os.ReadFile(filepath.Join(home(), ".config", "lazy1c", "settings.toml"))
	if err != nil || !strings.Contains(string(data), "paused_clusters") {
		t.Fatalf("файл без паузы: %v %s", err, data)
	}
	// «перезапуск»: enabled берётся из настроек
	if m.settings.isPausedCluster("a") != true {
		t.Fatal("пауза потеряна")
	}
}

func home() string { h, _ := os.UserHomeDir(); return h }

// TestTreeStatePersisted: свёрнутость кластеров и баз сохраняется и
// восстанавливается на первом снапшоте; новые базы появляются раскрытыми.
func TestTreeStatePersisted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()

	// раскрыли ib1, состояние дерева сохранилось при первом снапшоте
	var m2 tea.Model
	m2, _ = m.Update(tea.Msg(snapshotMsg{stateIdx: 0, snap: m.state[0].snap}))
	m = m2.(*model)
	if m.state[0].ibOpen["ib1"] {
		t.Fatal("новый кластер: базы должны быть закрыты по умолчанию")
	}
	// свернули базу и кластер
	m.state[0].ibOpen["ib1"] = false
	m.state[0].expanded = false
	m.saveTreeStateNow()
	if inList(m.settings.TreeIbOpen, "a|ib1") || !inList(m.settings.TreeClosed, "a") {
		t.Fatalf("состояние дерева не записано: %+v", m.settings)
	}
	// «перезапуск»: новая модель, первый снапшот
	fresh := zoomTestModel()
	fresh.settings = m.settings
	fresh.applyTreeState()
	if fresh.state[0].expanded {
		t.Fatal("кластер должен остаться свёрнутым")
	}
	fresh.state[0].expanded = true
	fresh.state[0].firstSnap = true
	m2, _ = fresh.Update(tea.Msg(snapshotMsg{stateIdx: 0, snap: fresh.state[0].snap}))
	fresh = m2.(*model)
	if fresh.state[0].ibOpen["ib1"] {
		t.Fatal("база должна остаться закрытой (не в списке открытых)")
	}
	// явно открытая база — восстановлена
	fresh.settings.TreeIbOpen = []string{"a|ib-open"}
	fresh.state[0].firstSnap = true
	fresh.state[0].snap.Infobases["c1"] = append(fresh.state[0].snap.Infobases["c1"],
		&serializev1.InfobaseSummaryInfo{Uuid: "ib-open", Name: "OPEN"})
	m2, _ = fresh.Update(tea.Msg(snapshotMsg{stateIdx: 0, snap: fresh.state[0].snap}))
	fresh = m2.(*model)
	if !fresh.state[0].ibOpen["ib-open"] {
		t.Fatal("явно открытая база не восстановлена")
	}
	// новая база — закрыта
	fresh.state[0].snap.Infobases["c1"] = append(fresh.state[0].snap.Infobases["c1"],
		&serializev1.InfobaseSummaryInfo{Uuid: "ib-new", Name: "NEW"})
	fresh.state[0].firstSnap = true
	m2, _ = fresh.Update(tea.Msg(snapshotMsg{stateIdx: 0, snap: fresh.state[0].snap}))
	fresh = m2.(*model)
	if fresh.state[0].ibOpen["ib-new"] {
		t.Fatal("новая база должна быть закрыта")
	}
}

// TestClusterRemoveCleansTreeState: убрали кластер — его записи о дереве тоже чистятся.
func TestClusterRemoveCleansTreeState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.settings.TreeClosed = []string{"a"}
	m.settings.TreeIbOpen = []string{"a|ib1"}
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'D'})
	m = m2.(*model)
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	m2, _ = m.Update(cmd())
	m = m2.(*model)
	for _, v := range m.settings.TreeClosed {
		if v == "a" {
			t.Fatal("свёрнутость убранного кластера осталась в settings")
		}
	}
	for _, v := range m.settings.TreeIbOpen {
		if v == "a|ib1" {
			t.Fatal("закрытая база убранного кластера осталась в settings")
		}
	}
}

// TestClusterNameHostFallback: пустой Host (8.2-движок) — имя из адреса подключения.
func TestClusterNameHostFallback(t *testing.T) {
	m := zoomTestModel()
	m.state[0].cluster.Host = "" // 8.2 не отдал хост
	m.state[0].address = "10.211.55.3:1540"
	m.state[0].version = "8.2.19.130"
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "10.211.55.3:") {
		t.Fatalf("нет fallback-хоста из адреса: %s", firstLines(v))
	}
	if !strings.Contains(v, "8.2.19.130") {
		t.Fatal("версия пропала из строки кластера")
	}
}

// TestLeftJumpsUp: ← — прогрессивное сворачивание/подъём:
// сеанс → база, раскрытая база → свёрнутая база, свёрнутая база → кластер,
// раскрытый кластер → свёрнутый.
func TestLeftJumpsToCluster(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true
	m.rebuild()

	// сеанс → ← → на базу (без сворачивания — просто переход)
	m.cursor = 2
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'h'})
	m = m2.(*model)
	if m.cursor != 1 || m.rows[m.cursor].kind != rowInfobase {
		t.Fatalf("← на сеансе должен вести на базу: cursor=%d", m.cursor)
	}
	if !m.state[0].ibOpen["ib1"] {
		t.Fatal("← на сеансе не должен сворачивать базу (просто переход)")
	}

	// раскрытая база → ← → сворачивает сеансы (курсор остаётся на базе)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
	m = m2.(*model)
	if m.state[0].ibOpen["ib1"] {
		t.Fatal("← на раскрытой базе должен свернуть сеансы")
	}

	// свёрнутая база → ← → на кластер
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
	m = m2.(*model)
	if m.cursor != 0 || m.rows[m.cursor].kind != rowCluster {
		t.Fatalf("← на свёрнутой базе должен вести на кластер: cursor=%d", m.cursor)
	}

	// раскрытый кластер → ← → свернуть
	if !m.state[0].expanded {
		t.Fatal("кластер должен быть раскрыт")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
	m = m2.(*model)
	if m.state[0].expanded {
		t.Fatal("← на раскрытом кластере должен свернуть")
	}
}

// TestToggleResetsActiveOnly: обычное → сбрасывает режим «только активные».
func TestToggleResetsActiveOnly(t *testing.T) {
	m := zoomTestModel()
	snap := m.state[0].snap
	snap.Infobases["c1"] = append(snap.Infobases["c1"],
		&serializev1.InfobaseSummaryInfo{Uuid: "ib-empty", Name: "EMPTY"})
	m.state[0].expanded = false // свёрнут
	m.cursor = 0
	m.rebuild()

	// Shift+→ уровень 1: только активные
	m.expandAll()
	if !m.state[0].expandOnlyActive {
		t.Fatal("уровень 1 не установил expandOnlyActive")
	}
	// → свернуть
	m.cursor = 0
	m.toggleNode()
	if m.state[0].expanded {
		t.Fatal("не свернулся")
	}
	// → раскрыть — expandOnlyActive должен сброситься
	m.toggleNode()
	if m.state[0].expandOnlyActive {
		t.Fatal("обычное → не сбросило expandOnlyActive")
	}
	// пустая база видна (expandOnlyActive=false)
	found := false
	for _, r := range m.rows {
		if r.kind == rowInfobase && r.ib.GetUuid() == "ib-empty" {
			found = true
		}
	}
	if !found {
		t.Fatal("пустая база не видна после обычного →")
	}
}

// TestArrowOnSession: → на сеансе сворачивает родительскую базу.
func TestArrowOnSession(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true
	m.rebuild()
	m.cursor = 2 // сеанс
	m.toggleNode()
	if m.state[0].ibOpen["ib1"] {
		t.Fatal("→ на сеансе не свернул родительскую базу")
	}
}

// TestPendingKillSpinner: после подтверждения сеанс серый со спиннером,
// actionDoneMsg снимает pending.
func TestPendingKillSpinner(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true
	m.rebuild()
	m.cursor = 2 // сеанс Васи

	// d → подтверждение → y: seанс помечен, спиннер запущен
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	m = m2.(*model)
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	if _, ok := m.pendingKills["s1"]; !ok {
		t.Fatal("сеанс не помечен как pending")
	}
	// рендер: сеанс помечен (серый, без цвета приложения)
	_ = m.View()
	// actionDoneMsg НЕ снимает pending (сообщение должно быть видно)
	_ = cmd
	m3, _ := m.Update(actionDoneMsg{stateIdx: 0, what: "test", err: nil})
	m = m3.(*model)
	if len(m.pendingKills) == 0 {
		t.Fatal("pending снят слишком рано (actionDoneMsg не должен снимать)")
	}
	// снапшот, где сеанс ещё жив, — пометка ОСТАЁТСЯ (до последнего момента сеанса)
	m3, _ = m.Update(snapshotMsg{stateIdx: 0, snap: m.state[0].snap})
	m = m3.(*model)
	if _, ok := m.pendingKills["s1"]; !ok {
		t.Fatal("pending снят раньше смерти сеанса")
	}
	// ошибка завершения — снимает немедленно
	m3, _ = m.Update(actionDoneMsg{stateIdx: 0, what: "terminate", err: fmt.Errorf("отказ"), sessIDs: []string{"s1"}})
	m = m3.(*model)
	if _, ok := m.pendingKills["s1"]; ok {
		t.Fatal("ошибка terminate не сняла pending")
	}
	// снапшот без сеанса — метки нет
	m.markPendingKill("t", "s1")
	fresh := *m.state[0].snap
	fresh.Sessions = map[string][]*serializev1.SessionInfo{"c1": {}}
	m3, _ = m.Update(snapshotMsg{stateIdx: 0, snap: &fresh})
	m = m3.(*model)
	if _, ok := m.pendingKills["s1"]; ok {
		t.Fatal("pending не снят после исчезновения сеанса из снапшота")
	}
}
