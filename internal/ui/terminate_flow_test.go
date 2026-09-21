package ui

// Сквозной тест завершения отмеченных сеансов: фейковый движок считает
// вызовы TerminateSession — проверяем, что после «d → y» реально уходят
// terminate-запросы по каждому отмеченному uuid (а не только пропадают ✓).

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/ras"
)

// fakeConn — движок-счётчик: всё успешно, TerminateSession записывается.
type fakeConn struct {
	terminated []string // uuid в порядке вызова
	failOn     map[string]error
}

func (f *fakeConn) Poll(context.Context, ras.Creds, ras.Creds, map[string]ras.Creds) (*ras.Snapshot, error) {
	return nil, nil
}
func (f *fakeConn) AgentVersion(context.Context) (string, error) { return "8.2.19", nil }
func (f *fakeConn) GetInfobase(context.Context, string, string) (*serializev1.InfobaseInfo, error) {
	return nil, nil
}
func (f *fakeConn) GetInfobaseAuth(context.Context, string, string, ras.Creds) (*serializev1.InfobaseInfo, error) {
	return nil, nil
}
func (f *fakeConn) UpdateInfobase(context.Context, *messagesv1.UpdateInfobaseRequest) error { return nil }
func (f *fakeConn) DisconnectConnection(context.Context, string, string, string) error     { return nil }
func (f *fakeConn) TerminateSession(_ context.Context, _, sessionID, _ string) error {
	if err, ok := f.failOn[sessionID]; ok {
		return err
	}
	f.terminated = append(f.terminated, sessionID)
	return nil
}

// runCmd — выполнить команду в тесте: батч мог схлопнуться в одно
// сообщение (compactCmds), поэтому обрабатываем обе формы; все msg — в модель.
func runCmd(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	var deliver func(tea.Msg)
	deliver = func(msg tea.Msg) {
		if msg == nil {
			return
		}
		switch msg.(type) {
		case tickMsg, spinTickMsg, bulkDoneMsg:
			return // таймеры в тесте не гоняем
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				deliver(sub())
			}
			return
		}
		m2, _ := m.Update(msg)
		_ = m2
	}
	deliver(cmd())
}

// TestMarkedTerminateActuallyTerminates: d по отметкам + «да» → движок
// получает terminate по каждому отмеченному сеансу, отметки сняты,
// pendingKills стоят, actionDoneMsg чистит всё после свежего снапшота.
func TestMarkedTerminateActuallyTerminates(t *testing.T) {
	fake := &fakeConn{}
	m := markTestModel()
	m.state[0].conn = fake
	m.settings = Settings{}

	m.cursor = sessionRow(t, m, "s1")
	m.Update(tea.KeyPressMsg{Code: ' '}) // ✓ s1
	m.Update(tea.KeyPressMsg{Code: ' '}) // ✓ s2
	m.Update(tea.KeyPressMsg{Code: 'd'})
	if m.confirm == nil {
		t.Fatal("нет подтверждения")
	}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("«да» не вернуло команду — terminate не выполняется")
	}
	runCmd(t, m, cmd) // в реальном приложении команды гоняет bubbletea
	if len(fake.terminated) != 2 {
		t.Fatalf("после «да» terminate не ушёл в движок (вызовов %d): клиент не завершает сеансы", len(fake.terminated))
	}
	for _, id := range []string{"s1", "s2"} {
		found := false
		for _, x := range fake.terminated {
			if x == id {
				found = true
			}
		}
		if !found {
			t.Errorf("terminate по %s не вызван (было: %v)", id, fake.terminated)
		}
	}
	if len(m.marked) != 0 || len(m.pendingKills) != 2 {
		t.Fatalf("отметки/метки не в работе: marked=%v pending=%v", m.marked, m.pendingKills)
	}

	// ошибки нет → pendingKills ждут свежего снапшота (метки «завершается»)
	if len(m.pendingKills) != 2 {
		t.Fatalf("после успешного actionDone метки не должны сниматься: %v", m.pendingKills)
	}

	// свежий снапшот без жертв → метки снялись
	fresh := &ras.Snapshot{
		Clusters:  m.state[0].snap.Clusters,
		Infobases: m.state[0].snap.Infobases,
		Sessions:  map[string][]*serializev1.SessionInfo{"c1": {}},
	}
	m.Update(snapshotMsg{stateIdx: 0, snap: fresh})
	if len(m.pendingKills) != 0 {
		t.Fatalf("метки не сняты свежим снапшотом: %v", m.pendingKills)
	}
}

// TestMarkedTerminateErrorKeepsMarksVisible: ошибка завершения видна —
// лог с ОШИБКОЙ, метки сняты (сеанс жив, строка возвращается к обычному виду).
func TestMarkedTerminateErrorKeepsMarksVisible(t *testing.T) {
	fake := &fakeConn{failOn: map[string]error{"s2": context.DeadlineExceeded}}
	m := markTestModel()
	m.state[0].conn = fake
	m.settings = Settings{}

	m.marked["s1"] = "t"
	m.marked["s2"] = "t"
	m.Update(tea.KeyPressMsg{Code: 'd'})
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("нет команды")
	}
	runCmd(t, m, cmd)
	if len(fake.terminated) != 1 || fake.terminated[0] != "s1" {
		t.Fatalf("должен завершиться только живой s1: %v", fake.terminated)
	}
	hasErr := false
	for _, l := range m.log {
		if strings.Contains(l, "ОШИБКА") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Fatal("ошибка завершения не попала в журнал")
	}
}

// TestTerminateStatusLifecycle: статусы внизу окна — крутилка «Завершаем
// сеансы», затем 3 секунды «Сеансы завершены», затем обычная полоска.
func TestTerminateStatusLifecycle(t *testing.T) {
	fake := &fakeConn{}
	m := markTestModel()
	m.state[0].conn = fake
	m.settings = Settings{}

	m.marked["s1"] = "t"
	m.marked["s2"] = "t"
	m.Update(tea.KeyPressMsg{Code: 'd'})
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	runCmd(t, m, cmd)

	// идёт завершение: статус с крутилкой и счётчиком, строка сеанса — с «завершается»
	if st := ansi.Strip(m.renderStatus()); !strings.Contains(st, "Завершаем сеансы (2)") {
		t.Fatalf("нет статуса «Завершаем сеансы (2)»: %q", st)
	}
	if m.Update(spinTickMsg{}); m.spinStep == 0 {
		t.Fatal("крутилка не анимируется")
	}
	if _, plain := m.rowText(&m.rows[sessionRow(t, m, "s1")]); !strings.Contains(plain, "завершается") {
		t.Fatalf("в строке сеанса нет крутилки завершения: %q", plain)
	}

	// свежий снапшот без жертв → метки сняты, статус успеха (зелёный, 3с)
	fresh := &ras.Snapshot{
		Clusters:  m.state[0].snap.Clusters,
		Infobases: m.state[0].snap.Infobases,
		Sessions:  map[string][]*serializev1.SessionInfo{"c1": {}},
	}
	m.Update(snapshotMsg{stateIdx: 0, snap: fresh})
	if st := ansi.Strip(m.renderStatus()); !strings.Contains(st, "Сеансы завершены") {
		t.Fatalf("нет статусa «Сеансы завершены»: %q", st)
	}

	// срок вышел — обычная полоска подсказок
	m.Update(bulkDoneMsg{at: m.bulkDoneAt})
	if st := ansi.Strip(m.renderStatus()); strings.Contains(st, "Сеансы завершены") {
		t.Fatalf("статус успеха должен уйти: %q", st)
	}
}

// TestKillTimeoutReturnsMarks: сеансы не исчезли из снапшота дольше
// killTimeout — крутилка останавливается, отметки ✓ возвращаются (можно
// повторить d), статус «не завершились» уходит через 3 секунды.
func TestKillTimeoutReturnsMarks(t *testing.T) {
	fake := &fakeConn{failOn: map[string]error{}} // ack есть, но сеанс «жив» — снапшот не меняется
	m := markTestModel()
	m.state[0].conn = fake
	m.settings = Settings{}

	m.marked["s1"] = "t"
	m.marked["s2"] = "t"
	m.Update(tea.KeyPressMsg{Code: 'd'})
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	runCmd(t, m, cmd)
	if len(m.pendingKills) != 2 {
		t.Fatalf("метки не встали: %v", m.pendingKills)
	}

	// тайм-аут истёк (снапшоты не приносили исчезновений)
	m.killStarted = time.Now().Add(-killTimeout - time.Second)
	m.Update(spinTickMsg{})
	if len(m.pendingKills) != 0 {
		t.Fatalf("крутилка не остановилась по тайм-ауту: %v", m.pendingKills)
	}
	if len(m.marked) != 2 {
		t.Fatal("отметки не возвращены для повтора")
	}
	if st := ansi.Strip(m.renderStatus()); !strings.Contains(st, "не завершились") {
		t.Fatalf("нет статуса неудачи: %q", st)
	}
	m.Update(bulkFailMsg{at: m.bulkFailAt})
	if st := ansi.Strip(m.renderStatus()); strings.Contains(st, "не завершились") {
		t.Fatalf("статус неудачи должен уйти: %q", st)
	}
}
