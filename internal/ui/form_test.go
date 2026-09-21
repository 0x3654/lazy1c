package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestCredsForm: вкладка Свойства с ошибкой прав открывает форму,
// ввод логина/пароля с галкой сохраняет креды с привязкой кластер→база.
func TestCredsForm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.formDismissed = map[string]bool{}
	m.ibInfo["t/ib1"] = &ibEntry{errText: "Недостаточно прав пользователя на информационную базу"}
	m.cursor = 1 // база B
	m.rebuild()
	m.cursor = 1

	// ] → вкладка Свойства → форма открывается автоматически
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x5D})
	m = m2.(*model)
	if m.form == nil {
		t.Fatal("форма логина не открылась на вкладке Свойства с ошибкой прав")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"Доступ к базе B", "Логин", "Пароль", "сохранить"} {
		if !strings.Contains(v, want) {
			t.Errorf("в форме нет %q", want)
		}
	}
	// ввод: "admin", enter, "secret", enter, enter (на чекбоксе — ок)
	for _, ch := range "admin" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // на пароль
	m = m2.(*model)
	for _, ch := range "secret" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // на чекбокс
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // ок
	m = m2.(*model)
	if m.form != nil {
		t.Fatal("форма не закрылась по ok")
	}
	// креды сохранены с привязкой
	cr, ok := m.settings.ibCred("t", "B")
	if !ok || cr.User != "admin" || cr.Pwd != "secret" {
		t.Fatalf("креды не сохранены: %+v", cr)
	}
	// повторное открытие Свойств не показывает форму: креды уже есть
	m.cursor = 1
	m.detailTab = 0
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x5D})
	m = m2.(*model)
	if m.form != nil {
		t.Fatal("форма не должна навязываться при сохранённых кредах")
	}
}

// TestCredsFormDismiss: esc закрывает форму и не даёт ей открыться снова.
func TestCredsFormDismiss(t *testing.T) {
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.formDismissed = map[string]bool{}
	m.ibInfo["t/ib1"] = &ibEntry{errText: "Недостаточно прав"}
	m.cursor = 1
	m.rebuild()
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x5D}) // Свойства → форма
	m = m2.(*model)
	if m.form == nil {
		t.Fatal("форма не открылась")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B}) // esc
	m = m2.(*model)
	if m.form != nil || !m.formDismissed["t/B"] {
		t.Fatal("esc не закрыл форму с запоминанием")
	}
	m.cursor = 1
	m.detailTab = 0
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x5D})
	m = m2.(*model)
	if m.form != nil {
		t.Fatal("отменённая форма не должна открываться сама")
	}
	// ручного хоткея формы кредов больше нет: a добавляет кластер,
	// форма базы — только из «Свойств» при ошибке прав (пока не закрыта)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x61})
	m = m2.(*model)
	if m.form != nil || m.cform == nil {
		t.Fatal("a должен открывать форму добавления кластера, а не кредов")
	}
}

// TestCredsMenuSection: в меню x есть раздел Креды ИБ с дефолтом кластера,
// enter на нём открывает форму, сохранение пишет дефолт (Base="*").
func TestCredsMenuSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.formDismissed = map[string]bool{}
	m.cursor = 0

	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x78}) // x
	m = m2.(*model)
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Настройки - Креды") {
		t.Fatalf("табы не горизонтально в шапке: %s", firstLines(v))
	}
	// креды — на втором табе
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x5D}) // ] — таб Креды
	m = m2.(*model)
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Креды ИБ") || !strings.Contains(v, "Дефолт ИБ") {
		t.Fatalf("на табе кредов нет содержимого: %s", firstLines(v))
	}
	// дефолт ИБ — первый выбираемый на табе кредов
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter — открыть форму
	m = m2.(*model)
	if m.form == nil || m.form.target != "default" {
		t.Fatal("enter на дефолте не открыл форму")
	}
	_ = v
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Дефолт ИБ на все кластеры") {
		t.Errorf("форма не про дефолт: %s", firstLines(v))
	}
	// ввод admin/pass + ок
	for _, ch := range "admin" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // на пароль
	m = m2.(*model)
	for _, ch := range "pass" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // на галку
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // ок
	m = m2.(*model)
	d, ok := m.settings.ibDefault()
	if !ok || d.User != "admin" || d.Pwd != "pass" {
		t.Fatalf("дефолт не сохранён: %+v", d)
	}
	// правка кредов базы — через меню x → Креды; a теперь добавляет кластер
	m.cursor = 1
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x61})
	m = m2.(*model)
	if m.form != nil || m.cform == nil {
		t.Fatal("a должен открывать форму добавления кластера, а не кредов базы")
	}
	m.Update(tea.KeyPressMsg{Code: 0x1B}) // закрыть форму кластера
	m.openCredsForm("t", "B")
	if m.form == nil || m.form.target != "base" {
		t.Fatalf("openCredsForm не открывает форму базы: %+v", m.form)
	}
}

// TestKeyNormAnyLayout: русская раскладка — «в» работает как d, «с» как menu-x нет,
// а «ч» открывает меню (x) и «з»/] листает вкладки (p — нет, проверим k=л).
func TestKeyNormAnyLayout(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true
	m.cursor = 2 // сеанс Васи
	m.rebuild()
	// «в» = d на сеансе → подтверждение
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'в'})
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("русская «в» не сработала как d")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'n'})
	m = m2.(*model)
	// «ч» = x → меню
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'ч'})
	m = m2.(*model)
	if m.menu == nil {
		t.Fatal("русская «ч» не открыла меню (x)")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B})
	m = m2.(*model)
	// «л» = k — вверх
	m.cursor = 2
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'л'})
	m = m2.(*model)
	if m.cursor != 1 {
		t.Fatal("русская «л» не сработала как k")
	}
}

// TestPasswordMasked: пароль в форме отображается точками, не открытым текстом.
func TestPasswordMasked(t *testing.T) {
	m := zoomTestModel()
	m.settings = defaultSettings()
	m.formDismissed = map[string]bool{}
	m.ibInfo["t/ib1"] = &ibEntry{errText: "Недостаточно прав"}
	m.cursor = 1
	m.rebuild()
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x5D}) // Свойства → форма
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // на поле пароля
	m = m2.(*model)
	for _, ch := range "secret" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	v := ansi.Strip(m.View().Content)
	if strings.Contains(v, "secret") {
		t.Fatal("пароль отображается открытым текстом")
	}
	if !strings.Contains(v, "••••••") {
		t.Fatal("пароль не замаскирован точками")
	}
}
