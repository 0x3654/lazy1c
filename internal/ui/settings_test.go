package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

func settingsTestModel() *model {
	m := filterTestModel()
	s := defaultSettings() // как при реальном запуске (loadSettings)
	s.HideRAS = false
	s.HideIdle = false
	m.settings = s
	return m
}

// TestMenuFlow: x открывает меню, пробел переключает чекбокс (не закрывая),
// enter применяет и закрывает, esc откатывает все переключения.
func TestMenuFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := settingsTestModel()
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "RAS") {
		t.Fatal("в исходном состоянии RAS-сеанс должен быть виден")
	}

	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x78}) // x — меню
	m = m2.(*model)
	if m.menu == nil {
		t.Fatal("x не открыл меню")
	}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"Скрывать RAS-сеансы", "Клавиши", "read-only", "завершить сеанс"} {
		if !strings.Contains(v, want) {
			t.Errorf("в меню нет %q", want)
		}
	}
	// первый чекбокс — «Показывать РЗ/Вход», ниже HideRAS: j + пробел
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20})
	m = m2.(*model)
	if !m.settings.HideRAS {
		t.Fatal("пробел не переключил чекбокс")
	}
	if m.menu == nil {
		t.Fatal("пробел не должен закрывать меню")
	}
	// enter — применить и закрыть
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.menu != nil {
		t.Fatal("enter не закрыл меню")
	}
	if !m.settings.HideRAS {
		t.Fatal("enter не сохранил переключение")
	}
	if v := ansi.Strip(m.View().Content); strings.Contains(v, "RAS —@") {
		t.Error("RAS-сеанс должен скрыться после применения (бейдж RAS в шапке — не сеанс)")
	}
	// сохранение в файл
	data, err := os.ReadFile(filepath.Join(home, ".config", "lazy1c", "settings.toml"))
	if err != nil || !strings.Contains(string(data), "hide_ras_sessions = true") {
		t.Errorf("настройка не сохранена: %v %s", err, data)
	}

	// esc — отмена: переключили и откатили
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x78})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // на HideRAS
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20}) // выключили HideRAS
	m = m2.(*model)
	if m.settings.HideRAS {
		t.Fatal("пробел не переключил обратно")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B}) // esc — отмена
	m = m2.(*model)
	if !m.settings.HideRAS {
		t.Fatal("esc не откатил переключение к снимку")
	}
}

// TestReadOnlyMode: спец-режим запрещает d/r/b и чужие настройки,
// но позволяет снять сам read-only; бейджи видны в шапке.
func TestReadOnlyMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// мягкий режим (из настроек)
	m := settingsTestModel()
	m.state[0].ibOpen["ib-erp"] = true
	m.cursor = 2 // сеанс Васи
	m.settings.ReadOnly = true
	m.settings.HideIdle = true
	m.rebuild()
	m.cursor = 2
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "read-only") || !strings.Contains(v, "Zzz") {
		t.Errorf("бейджи спец-режима/спящих не в шапке: %s", firstLines(v))
	}
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x64}) // d
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("read-only должен запрещать завершение сеанса")
	}
	// r на базе тоже запрещено
	m.cursor = 1
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x72})
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("read-only должен запрещать переключение свойств базы")
	}
	// меню: локальные настройки видимости в read-only менять МОЖНО (не трогают кластер)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x78})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // на HideRAS (после «Показать»)
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20})
	m = m2.(*model)
	if !m.settings.HideRAS {
		t.Fatal("настройки видимости должны работать в read-only")
	}
	// снять сам read-only: вернуть RAS, дойти до RO (6-й от нуля)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20}) // RAS обратно
	m = m2.(*model)
	for i := 0; i < 5; i++ {
		m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // j: RAS→…→RO
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20})
	m = m2.(*model)
	if m.settings.ReadOnly {
		t.Fatal("снятие read-only из меню должно быть разрешено")
	}

	// жёсткий режим (флаг): пункт read-only неактивен, видимость по-прежнему можно
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B}) // esc
	m = m2.(*model)
	m.cfg.ReadOnly = true
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x78})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // на HideRAS
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20}) // HideRAS — можно
	m = m2.(*model)
	if !m.settings.HideRAS {
		t.Fatal("при флаге read-only видимость всё равно настраивается")
	}
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "(задан флагом)") {
		t.Fatal("у пункта read-only нет пометки про флаг")
	}
}

// TestBulkMenu: B без отметок — предупреждение и никаких действий;
// с отметками — завершение отмеченных со счётчиком и списком, n отменяет,
// read-only блокирует.
func TestBulkMenu(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := settingsTestModel()
	snap := m.state[0].snap
	snap.Sessions["c1"] = append(snap.Sessions["c1"],
		&serializev1.SessionInfo{Uuid: "s-sl", InfobaseId: "ib-erp", AppId: "1CV8C",
			UserName: "Соня", Host: "h", Hibernate: true})
	m.cursor = 0
	m.rebuild()

	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x42}) // B
	m = m2.(*model)
	if m.menu == nil || m.menu.kind != "bulk" {
		t.Fatal("B не открыл bulk-меню")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"Массовые операции", "Завершить отмеченные сеансы (0)", "Снять все отметки", "нет отмеченных"} {
		if !strings.Contains(v, want) {
			t.Errorf("в bulk-меню нет %q", want)
		}
	}
	// без отметок enter ничего не делает
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("без отметок bulk не должен завершать сеансы")
	}

	// с отметками — подтверждение со счётчиком и списком жертв
	m.marked = map[string]string{"s1": "t", "s-sl": "t"}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x42})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("массовое действие не запросило подтверждение")
	}
	if !strings.Contains(m.confirm.question, "2 сеанс") || !strings.Contains(m.confirm.question, "Соня") {
		t.Fatalf("вопрос без счётчика/списка: %q", m.confirm.question)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6E}) // n — отмена
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("n не отменило массовое завершение")
	}

	// read-only: bulk запрещён
	m.settings.ReadOnly = true
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x42})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("read-only должен запрещать массовое завершение")
	}
}

// TestSettingsRoundTrip: enter пишет файл, следующий запуск читает его.
func TestSettingsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := settingsTestModel()
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x78}) // x
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // ↓ на HideRAS (первый чекбокс — «Показать»)
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20}) // HideRAS → true
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter — записать
	m = m2.(*model)

	loaded := loadSettings() // «перезапуск»
	if !loaded.HideRAS {
		t.Fatal("после перезапуска HideRAS не прочитан из файла")
	}
	if !loaded.ShowFlags {
		t.Fatal("ShowFlags должен остаться true (дефолт, в файле мог не быть)")
	}
}

// TestBulkMenuEscKeepsSettings: esc в bulk-меню не должен затирать настройки
// (снимок есть только у меню настроек) — иначе следующее сохранение
// обнуляло settings.toml.
func TestBulkMenuEscKeepsSettings(t *testing.T) {
	m := settingsTestModel()
	m.settings = defaultSettings()
	m.settings.HideRAS = false
	saved := m.settings

	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x42}) // B — bulk-меню
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x1B}) // esc
	m = m2.(*model)
	if m.settings.HideRAS != saved.HideRAS || !m.settings.ShowFlags {
		t.Fatalf("esc в bulk-меню затёр настройки: %+v", m.settings)
	}
	// и после сохранения (enter в меню настроек) файл не обнуляется
	t.Setenv("HOME", t.TempDir())
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x78}) // x
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter — записать
	m = m2.(*model)
	loaded := loadSettings()
	if !loaded.ShowFlags {
		t.Fatal("после bulk-esc + сохранения настройки потеряны")
	}
}

// TestBulkMenuMarkedScope: отметки собираются по глазам независимо от баз —
// в превью каждая строка называет свою базу.
func TestBulkMenuMarkedScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := settingsTestModel() // erp: Вася+RAS, trade: Петя
	m.cursor = 1
	m.rebuild()
	m.marked = map[string]string{"s1": "t", "s2": "t"} // Вася (erp) + Петя (trade)

	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x42}) // B
	m = m2.(*model)
	if m.menu == nil || m.menu.kind != "bulk" {
		t.Fatal("B не открыл bulk-меню")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "отмечено 2") {
		t.Errorf("число отметок не в шапке: %s", firstLines(v))
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // «Завершить отмеченные (2)»
	m = m2.(*model)
	if m.confirm == nil || !strings.Contains(m.confirm.question, "2 сеанс") {
		t.Fatalf("отметки не собрались: %q", m.confirm.question)
	}
	if !strings.Contains(m.confirm.question, "erp_demo") || !strings.Contains(m.confirm.question, "trade_opti") {
		t.Fatalf("в превью нет имён баз: %q", m.confirm.question)
	}
}

// TestAskButtonsDefaultsCancel: горизонтальное ДА/ОТМЕНА для особо опасных
// операций — по умолчанию ОТМЕНА, enter на ней ничего не выполняет.
func TestAskButtonsDefaultsCancel(t *testing.T) {
	m := settingsTestModel()
	ran := false
	m.askButtons("Точно?", func() tea.Cmd { ran = true; return nil })
	if m.confirm == nil || !m.confirm.buttons || m.confirm.choice != 1 {
		t.Fatal("askButtons не выставил ОТМЕНА по умолчанию")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "ДА") || !strings.Contains(v, "ОТМЕНА") {
		t.Fatalf("кнопок нет на экране: %s", firstLines(v))
	}
	m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter на ОТМЕНЕ
	if m.confirm != nil || ran {
		t.Fatal("enter на ОТМЕНЕ не должен выполнять действие")
	}
}
