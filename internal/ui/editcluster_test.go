package ui

// Тесты окна реквизитов кластера (e): только показ — имя записи, адрес,
// версия, серверная личность, счётчики и состояние опроса.

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// editTestModel — кластер t по адресу a:1545 (как в реальном конфиге).
func editTestModel() *model {
	m := markTestModel()
	m.state[0].address = "a:1545"
	m.rebuild()
	return m
}

// TestClusterInfoWindow: e открывает окно с реквизитами, esc закрывает,
// не на кластере — ничего.
func TestClusterInfoWindow(t *testing.T) {
	m := editTestModel() // кластер t по адресу a:1545, база B, сеансы s1/s2
	m.state[0].version = "8.3.27.2325"

	m.cursor = 0
	m.Update(tea.KeyPressMsg{Code: 'e'})
	if !m.eshow {
		t.Fatal("e на кластере не открыл окно реквизитов")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Кластер",
		"Имя (настройки)", "t",
		"Адрес", "a:1545",
		"Версия 1С", "8.3.27.2325",
		"Движок",
		"Кластер (сервер)", "K",
		"UUID кластера",
		"Базы 1", "Сеансы 2 (видно 2)",
		"Опрос:",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("в окне реквизитов нет %q:\n%s", want, firstLines(v))
		}
	}
	// редактирования нет — только закрытие
	m.Update(tea.KeyPressMsg{Code: 0x1B})
	if m.eshow {
		t.Fatal("esc не закрыл окно")
	}

	// не на кластере e ничего не делает
	m.cursor = sessionRow(t, m, "s1")
	m.Update(tea.KeyPressMsg{Code: 'e'})
	if m.eshow {
		t.Fatal("e на сеансе не должен открывать окно кластера")
	}
}

// TestClusterInfoWindowEmptyFields: без версии/снапшота — прочерки и «нет данных»,
// а не пустые строки.
func TestClusterInfoWindowEmptyFields(t *testing.T) {
	m := editTestModel()
	m.state[0].version = ""
	m.state[0].snap = nil
	m.cursor = 0
	m.Update(tea.KeyPressMsg{Code: 'e'})
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Версия 1С      —") && !strings.Contains(v, "—") {
		t.Fatalf("без версии должен быть прочерк: %s", firstLines(v))
	}
	if !strings.Contains(v, "нет данных опроса") {
		t.Fatalf("без снапшота должна быть заглушка: %s", firstLines(v))
	}
}

// TestClusterInfoWindowPortless: голое имя в адресе показывается с портом 1540.
func TestClusterInfoWindowPortless(t *testing.T) {
	m := markTestModel() // адрес "a" — без порта
	m.cursor = 0
	m.Update(tea.KeyPressMsg{Code: 'e'})
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "a:1540") {
		t.Fatalf("голому имени должен дописываться порт: %s", firstLines(v))
	}
}
