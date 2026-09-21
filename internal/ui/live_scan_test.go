package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestDiscoverLiveLocalhost — живой скан 127.0.0.1 (докер-порты проброшены);
// быстрая локальная проверка связки startScan → scanDoneMsg → список → добавление.
func TestDiscoverLiveLocalhost(t *testing.T) {
	m := zoomTestModel()
	m.settings = defaultSettings()
	cmd := m.startScan("127.0.0.1/32")
	if cmd == nil || m.scan == nil || !m.scan.running {
		t.Fatal("скан не запустился")
	}
	msg := cmd()
	m2, _ := m.Update(msg)
	m = m2.(*model)
	if m.scan == nil || m.scan.running {
		t.Fatal("скан не завершился")
	}
	if len(m.scan.results) == 0 {
		t.Skip("на localhost нет RAS (докер не запущен?) — пропускаю живую проверку")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Discover") || !strings.Contains(v, "127.0.0.1:") {
		t.Fatalf("находки не отрисованы: %s", firstLines(v))
	}
	// enter — добавить всё выбранное
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.scan != nil {
		t.Fatal("окно скана не закрылось")
	}
	if len(m.state) < 2 {
		t.Fatalf("кластеры не добавлены: %d", len(m.state))
	}
	for i := 1; i < len(m.state); i++ {
		if !strings.HasPrefix(m.state[i].address, "127.0.0.1:") {
			t.Fatalf("чужой адрес: %s", m.state[i].address)
		}
	}
}
