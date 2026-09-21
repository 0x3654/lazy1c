package ui

// Тесты построчного мультивыбора пробелом (✓): отметка сеансов/базы,
// пауза кластера пробелом, d/B по отметкам, esc, вставка в форму кластера.

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/ras"
)

// markTestModel — кластер t, база B (раскрыта), сеансы s1, s2.
func markTestModel() *model {
	m := zoomTestModel()
	m.settings = Settings{}
	m.marked = map[string]string{}
	m.state[0].ibOpen["ib1"] = true
	m.rebuild()
	return m
}

func sessionRow(t *testing.T, m *model, uuid string) int {
	t.Helper()
	for i := range m.rows {
		if m.rows[i].kind == rowSession && m.rows[i].session.GetUuid() == uuid {
			return i
		}
	}
	t.Fatalf("строка сеанса %s не найдена", uuid)
	return -1
}

// TestSpaceMarksSession: пробел отмечает сеанс ✓ и сдвигает курсор вниз;
// повторный пробел (вернувшись) — снимает отметку.
func TestSpaceMarksSession(t *testing.T) {
	m := markTestModel()
	m.cursor = sessionRow(t, m, "s1")
	m.Update(tea.KeyPressMsg{Code: ' '})
	if _, ok := m.marked["s1"]; !ok {
		t.Fatal("пробел не отметил сеанс s1")
	}
	if m.cursor != sessionRow(t, m, "s2") {
		t.Fatalf("курсор не сдвинулся на строку вниз: %d", m.cursor)
	}
	if _, plain := m.rowText(&m.rows[sessionRow(t, m, "s1")]); !strings.Contains(plain, "✓") {
		t.Errorf("в строке отмеченного сеанса нет ✓: %q", plain)
	}
	// вернуться и снять отметку
	m.Update(tea.KeyPressMsg{Code: 'k'})
	m.Update(tea.KeyPressMsg{Code: ' '})
	if _, ok := m.marked["s1"]; ok {
		t.Fatal("повторный пробел не снял отметку")
	}
	if _, plain := m.rowText(&m.rows[sessionRow(t, m, "s1")]); strings.Contains(plain, "✓") {
		t.Errorf("✓ остался в строке после снятия: %q", plain)
	}
}

// TestSpaceMarkAllBase: пробел на базе отмечает все её видимые сеансы,
// повторный — снимает все.
func TestSpaceMarkAllBase(t *testing.T) {
	m := markTestModel()
	m.cursor = 1 // база B
	m.Update(tea.KeyPressMsg{Code: ' '})
	if len(m.marked) != 2 {
		t.Fatalf("пробел на базе не отметил оба сеанса: %v", m.marked)
	}
	m.Update(tea.KeyPressMsg{Code: ' '})
	if len(m.marked) != 0 {
		t.Fatalf("повторный пробел на базе не снял отметки: %v", m.marked)
	}
}

// TestSpaceClusterPause: пробел на кластере — пауза мониторинга, как s.
func TestSpaceClusterPause(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := markTestModel()
	m.cursor = 0
	m.Update(tea.KeyPressMsg{Code: ' '})
	if m.state[0].enabled {
		t.Fatal("пробел на кластере не поставил паузу")
	}
	if !m.settings.isPausedCluster("a") {
		t.Fatal("пауза не ушла в настройки")
	}
	m.Update(tea.KeyPressMsg{Code: ' '})
	if !m.state[0].enabled {
		t.Fatal("повторный пробел не снял паузу")
	}
}

// TestTerminateMarked: d с отметками — подтверждение с числом и списком;
// «да» снимает отметки и ставит pendingKills.
func TestTerminateMarked(t *testing.T) {
	m := markTestModel()
	m.cursor = sessionRow(t, m, "s1")
	m.Update(tea.KeyPressMsg{Code: ' '}) // s1 ✓, курсор на s2
	m.Update(tea.KeyPressMsg{Code: ' '}) // s2 ✓
	m.Update(tea.KeyPressMsg{Code: 'd'})
	if m.confirm == nil {
		t.Fatal("d по отметкам не открыл подтверждение")
	}
	for _, want := range []string{"2 сеанс", "Вася", "Петя", "B"} {
		if !strings.Contains(m.confirm.question, want) {
			t.Errorf("в превью нет %q: %q", want, m.confirm.question)
		}
	}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("подтверждение не вернуло команду")
	}
	if len(m.marked) != 0 {
		t.Fatal("отметки не сняты после «да»")
	}
	if len(m.pendingKills) != 2 {
		t.Fatalf("pendingKills не получил оба сеанса: %v", m.pendingKills)
	}
}

// TestTerminateMarkedPreviewCap: больше 5 сеансов — список укорочен «… и ещё».
func TestTerminateMarkedPreviewCap(t *testing.T) {
	m := markTestModel()
	sessions := m.state[0].snap.Sessions["c1"]
	for i := 3; i <= 7; i++ {
		sessions = append(sessions, &serializev1.SessionInfo{
			Uuid: string(rune('0'+i)), InfobaseId: "ib1", AppId: "1CV8C", UserName: "u", Host: "h"})
	}
	m.state[0].snap.Sessions["c1"] = sessions
	for _, s := range sessions {
		m.marked[s.GetUuid()] = "t"
	}
	m.Update(tea.KeyPressMsg{Code: 'd'})
	if m.confirm == nil {
		t.Fatal("нет подтверждения")
	}
	if !strings.Contains(m.confirm.question, "… и ещё 2") {
		t.Errorf("превью не обрезано: %q", m.confirm.question)
	}
}

// TestBulkMenuRequiresMarks: B без отметок — меню с предупреждением,
// действия ничего не делают; с отметками — пункт выполняет завершение.
func TestBulkMenuRequiresMarks(t *testing.T) {
	m := markTestModel()
	m.Update(tea.KeyPressMsg{Code: 'B'})
	if m.menu == nil || m.menu.kind != "bulk" {
		t.Fatal("B не открыл bulk-меню")
	}
	if !strings.Contains(m.menu.notice, "нет отмеченных") {
		t.Fatalf("без отметок нет предупреждения: %q", m.menu.notice)
	}
	m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter на «Завершить отмеченные (0)»
	if m.confirm != nil {
		t.Fatal("без отметок bulk не должен завершать сеансы")
	}
	// с отметками — тот же enter открывает подтверждение
	m.marked["s1"] = "t"
	m.Update(tea.KeyPressMsg{Code: 'B'})
	m.Update(tea.KeyPressMsg{Code: 0x0D})
	if m.confirm == nil {
		t.Fatal("с отметками bulk не открыл подтверждение")
	}
}

// TestEscClearsMarks: esc снимает все отметки разом.
func TestEscClearsMarks(t *testing.T) {
	m := markTestModel()
	m.marked["s1"] = "t"
	m.marked["s2"] = "t"
	m.Update(tea.KeyPressMsg{Code: 0x1B})
	if len(m.marked) != 0 {
		t.Fatal("esc не снял отметки")
	}
}

// TestRemoveClusterOnlyOnClusterRow: D не на кластере — ничего не происходит.
func TestRemoveClusterOnlyOnClusterRow(t *testing.T) {
	m := markTestModel()
	m.cursor = sessionRow(t, m, "s1")
	m.Update(tea.KeyPressMsg{Code: 'D'})
	if m.confirm != nil {
		t.Fatal("D на сеансе не должен спрашивать про удаление кластера")
	}
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'D'})
	m = m2.(*model)
	if m.confirm == nil || !strings.Contains(m.confirm.question, "не затрагивается") {
		t.Fatal("D на кластере не спросил подтверждение")
	}
}

// TestAddClusterKey: a открывает форму, «+» больше не работает.
func TestAddClusterKey(t *testing.T) {
	m := markTestModel()
	m.Update(tea.KeyPressMsg{Code: 'a'})
	if m.cform == nil {
		t.Fatal("a не открыл форму добавления кластера")
	}
	m.Update(tea.KeyPressMsg{Code: 0x1B}) // esc — закрыть
	m.Update(tea.KeyPressMsg{Code: '+'})
	if m.cform != nil {
		t.Fatal("+ больше не должен открывать форму")
	}
}

// TestMarkPrunedBySnapshot: отметка сеанса, исчезнувшего из снапшота, снимается.
func TestMarkPrunedBySnapshot(t *testing.T) {
	m := markTestModel()
	m.marked["s1"] = "t"
	m.marked["s2"] = "t"
	fresh := &ras.Snapshot{
		Clusters:  m.state[0].snap.Clusters,
		Infobases: m.state[0].snap.Infobases,
		Sessions:  map[string][]*serializev1.SessionInfo{"c1": {m.state[0].snap.Sessions["c1"][1]}},
	}
	m2, _ := m.Update(snapshotMsg{stateIdx: 0, snap: fresh})
	m = m2.(*model)
	if _, ok := m.marked["s1"]; ok {
		t.Fatal("отметка умершего сеанса не снята")
	}
	if _, ok := m.marked["s2"]; !ok {
		t.Fatal("живой сеанс не должен терять отметку")
	}
}

// TestZoomSpaceMark: в полноэкранном режиме пробел отмечает, d завершает отмеченные.
func TestZoomSpaceMark(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter — zoom
	m = m2.(*model)
	if m.zoom == nil {
		t.Fatal("zoom не открылся")
	}
	m.Update(tea.KeyPressMsg{Code: ' '}) // s1 ✓, курсор на s2
	if _, ok := m.marked["s1"]; !ok {
		t.Fatal("пробел в zoom не отметил сеанс")
	}
	if m.zoom.cursor != 1 {
		t.Fatalf("курсор zoom не сдвинулся: %d", m.zoom.cursor)
	}
	m.Update(tea.KeyPressMsg{Code: ' '}) // s2 ✓
	m.Update(tea.KeyPressMsg{Code: 'd'})
	if m.confirm == nil || !strings.Contains(m.confirm.question, "2 сеанс") {
		t.Fatalf("d в zoom не собрал отметки: %v", m.confirm)
	}
}

// TestClusterPaste: вставка из буфера в форму добавления кластера.
func TestClusterPaste(t *testing.T) {
	m := markTestModel()
	m.Update(tea.KeyPressMsg{Code: 'a'})
	f := m.cform
	f.host = "old.example"

	// адрес:порт — целиком в поле хоста
	m2, _ := m.Update(tea.PasteMsg{Content: " srv-1c.local:1545 \n"})
	m = m2.(*model)
	if f.host != "srv-1c.local:1545" || f.field != 0 {
		t.Fatalf("адрес:порт не лег в хост: %q field=%d", f.host, f.field)
	}
	m.Update(tea.PasteMsg{Content: "[::1]:1540"})
	if f.host != "[::1]:1540" {
		t.Fatalf("IPv6 не лег в хост: %q", f.host)
	}
	// голый хост
	m.Update(tea.PasteMsg{Content: "nas.local"})
	if f.host != "nas.local" {
		t.Fatalf("голый хост не лег в поле: %q", f.host)
	}
	// чистые цифры — тоже хост (одиночный токен)
	m.Update(tea.PasteMsg{Content: "65535"})
	if f.host != "65535" {
		t.Fatalf("цифры должны идти в хост: %q", f.host)
	}
	// CIDR — в поле поиска
	m.Update(tea.PasteMsg{Content: "10.0.0.0/24,192.168.1.0/24"})
	if f.ranges != "10.0.0.0/24,192.168.1.0/24" || f.field != 1 {
		t.Fatalf("CIDR не лег в поле поиска: %q", f.ranges)
	}
	// мусор с пробелами — ничего не меняется
	f.host, f.ranges = "h", ""
	m.Update(tea.PasteMsg{Content: "два слова"})
	if f.host != "h" || f.ranges != "" {
		t.Fatalf("мусор должен игнорироваться: %q %q", f.host, f.ranges)
	}
}

