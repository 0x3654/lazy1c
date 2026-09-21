package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"lazy1c/internal/engine"
)

// Окно реквизитов кластера (e на строке кластера): только показываем то,
// что знаем о кластере — имя записи мониторинга, адрес подключения,
// версию платформы, серверную личность и счётчики. Редактирования нет:
// имя из настроек — чисто отображение, адрес задаётся при добавлении (a).

// updateClusterInfoWin — клавиши окна реквизитов: закрытие.
func (m *model) updateClusterInfoWin(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "enter", "e":
		m.eshow = false
	}
	return m, nil
}

// clusterInfoRows — строки «реквизит — значение» текущего кластера.
func (m *model) clusterInfoRows() [][2]string {
	r := m.curRow()
	if r == nil || r.kind != rowCluster {
		return nil
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return nil
	}
	addr := st.address
	if !strings.Contains(addr, ":") {
		addr += ":1540" // голое имя — стандартный порт ragent
	}
	version := st.version
	if version == "" {
		version = "—"
	}
	rows := [][2]string{
		{"Имя (настройки)", st.id},
		{"Адрес", addr},
		{"Версия 1С", version},
		{"Движок", engine.Kind(st.conn)},
	}
	if st.cluster != nil {
		name := st.cluster.GetName()
		if name == "" {
			name = "—"
		}
		host := st.cluster.GetHost()
		if host == "" {
			host = hostOf(st.address)
		}
		rows = append(rows,
			[2]string{"Кластер (сервер)", name},
			[2]string{"Адрес кластера", fmt.Sprintf("%s:%d", host, st.cluster.GetPort())},
			[2]string{"UUID кластера", shortUUID(st.cluster.GetUuid())},
		)
		// серверные свойства — показываем только заполненные (ras отдаёт,
		// MMC-движки пока нет): нули только шумят
		if v := st.cluster.GetMaxMemorySize(); v > 0 {
			rows = append(rows, [2]string{"Макс. память", fmt.Sprintf("%d МБ", v)})
		}
		if v := st.cluster.GetSecurityLevel(); v > 0 {
			rows = append(rows, [2]string{"Уровень защиты", fmt.Sprintf("%d", v)})
		}
		if v := st.cluster.GetLifetimeLimit(); v > 0 {
			rows = append(rows, [2]string{"Перерывы в работе", fmt.Sprintf("%d c", v)})
		}
		if v := st.cluster.GetSessionFaultToleranceLevel(); v > 0 {
			rows = append(rows, [2]string{"Отказоустойчивость", fmt.Sprintf("%d", v)})
		}
	}
	return rows
}

// clusterInfoCounts — сводка по снапшоту: базы/сеансы/соединения/…
func (m *model) clusterInfoCounts() string {
	r := m.curRow()
	if r == nil {
		return ""
	}
	st := m.stateByName(r.clusterID)
	if st == nil || st.snap == nil || st.cluster == nil {
		return "нет данных опроса"
	}
	cuuid := st.cluster.GetUuid()
	sessions := st.snap.Sessions[cuuid]
	shown := m.countShownSessions(sessions)
	parts := []string{
		fmt.Sprintf("Базы %d", len(st.snap.Infobases[cuuid])),
		fmt.Sprintf("Сеансы %d (видно %d)", len(sessions), shown),
		fmt.Sprintf("Соединения %d", len(st.snap.Connections[cuuid])),
		fmt.Sprintf("rphost %d", len(st.snap.Processes[cuuid])),
		fmt.Sprintf("Менеджеры %d", len(st.snap.Managers[cuuid])),
		fmt.Sprintf("Серверы %d", len(st.snap.Servers[cuuid])),
		fmt.Sprintf("Блокировки %d", len(st.snap.Locks[cuuid])),
	}
	return strings.Join(parts, " · ")
}

// clusterInfoPoll — строка состояния опроса кластера.
func (m *model) clusterInfoPoll(r *row) string {
	st := m.stateByName(r.clusterID)
	if st == nil {
		return ""
	}
	if st.lastErr != "" {
		return "Опрос: ошибка — " + st.lastErr
	}
	if st.snap == nil {
		return "Опрос: ещё нет данных"
	}
	status := "вкл"
	if !st.enabled {
		status = "пауза"
	}
	return fmt.Sprintf("Опрос: %s · %s назад · %dмс", status,
		ago(st.updated), st.snap.Took.Milliseconds())
}

// renderClusterInfoWin — оверлей «Кластер»: реквизиты, сводка, опрос.
func (m *model) renderClusterInfoWin() string {
	rows := m.clusterInfoRows()
	if rows == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(" Кластер") + "\n\n")
	for _, kv := range rows {
		b.WriteString(" " + truncPad(kv[0], 18) + " " + kv[1] + "\n")
	}
	r := m.curRow()
	b.WriteString("\n " + styleHint.Render(m.clusterInfoCounts()) + "\n")
	if r != nil {
		if s := m.clusterInfoPoll(r); s != "" {
			b.WriteString(" " + styleHint.Render(s) + "\n")
		}
	}
	b.WriteString("\n" + styleHint.Render(" esc — закрыть"))
	return withTitle(styleModal.Width(64).Render(b.String()), " e ")
}
