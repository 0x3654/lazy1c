package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// TestDetailTabs: [ ] листают вкладки, контент меняется, при смене строки — сброс.
func TestDetailTabs(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 0 // кластер
	m.rebuild()
	tabs := m.detailTabs()
	if len(tabs) != 6 {
		t.Fatalf("у кластера должно быть 6 вкладок, есть %d", len(tabs))
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Сводка - Сеансы") {
		t.Errorf("нет строки вкладок: %s", firstLines(v))
	}
	// ] → Сеансы, ]] → Процессы: rphost; таб-бар — заголовок окна
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x5D}) // ]
	m = m2.(*model)
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Сеансы кластера") {
		t.Errorf("вкладка Сеансы не открылась")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x5D}) // ] ещё раз → Процессы
	m = m2.(*model)
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "rphost") && !strings.Contains(v, "Рабочие процессы") {
		t.Errorf("вкладка Процессы не открылась")
	}
	// смена строки сбрасывает вкладку
	m.cursor = 1 // база
	m.rebuild()
	if m.detailTab != 0 {
		t.Fatal("вкладка не сбросилась при смене строки")
	}
	if tabs := m.detailTabs(); len(tabs) != 3 || tabs[1] != "Свойства" || tabs[2] != "Блокировки" {
		t.Fatalf("у базы должны быть вкладки Сводка/Свойства/Блокировки: %v", tabs)
	}
}

// TestInfobasePropsPendingAndError: свойства без кэша — «загружается», с ошибкой — карточка ошибки.
func TestInfobasePropsPendingAndError(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 1 // база B
	m.detailTab = 1
	m.rebuild()
	m.detailTab = 1 // rebuild сбросил — ставим снова (как будто пользователь нажал ])
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "загружается") {
		t.Error("без кэша свойства должны показывать «загружается…»")
	}
	m.ibInfo["t/ib1"] = &ibEntry{errText: "Недостаточно прав"}
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Недостаточно прав") {
		t.Error("ошибка свойств не показана")
	}
	// с данными — карточка
	m.ibInfo["t/ib1"] = &ibEntry{info: ibInfoFixture()}
	v = ansi.Strip(m.View().Content)
	for _, want := range []string{"PostgreSQL", "pg1c:5432", "запрещены", "разрешено"} {
		if !strings.Contains(v, want) {
			t.Errorf("в свойствах нет %q", want)
		}
	}
}

// TestIbToggleConfirm: r на базе открывает подтверждение с текущим состоянием.
func TestIbToggleConfirm(t *testing.T) {
	m := zoomTestModel()
	m.cursor = 1
	m.ibInfo["t/ib1"] = &ibEntry{info: ibInfoFixture()}
	m.rebuild()
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x72}) // r
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("r не открыл подтверждение")
	}
	q := m.confirm.question
	if !strings.Contains(q, "регламент") || !strings.Contains(q, "запрещены") {
		t.Fatalf("вопрос без текущего состояния: %q", q)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6E}) // n — отмена
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("n не отменил переключение")
	}
}

func ibInfoFixture() *serializev1.InfobaseInfo {
	return &serializev1.InfobaseInfo{
		Dbms: "PostgreSQL", DbServer: "pg1c:5432", DbName: "db_trade",
		Locale: "ru_RU", ScheduledJobsDeny: true, SessionsDeny: false,
	}
}

// TestBaseFlagsBadge: у базы в дереве видны статусы РЗ и Входа из карточки.
func TestBaseFlagsBadge(t *testing.T) {
	m := zoomTestModel()
	m.settings = defaultSettings() // ShowFlags включён по умолчанию
	m.ibInfo["t/ib1"] = &ibEntry{info: &serializev1.InfobaseInfo{
		ScheduledJobsDeny: true, SessionsDeny: false}}
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "РЗ:выкл") || !strings.Contains(v, "Вход:вкл") {
		t.Errorf("бейдж статусов не показан: %s", firstLines(v))
	}
	// обе запрещены — оба крестика
	m.ibInfo["t/ib1"].info.SessionsDeny = true
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Вход:выкл") {
		t.Error("запрет входа не отражён бейджем")
	}
}
