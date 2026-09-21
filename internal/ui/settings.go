package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbletea/v2"
	toml "github.com/pelletier/go-toml/v2"
)

// IBCred — сохранённые креды конкретной информационной базы
// (для баз со своим администратором).
type IBCred struct {
	Cluster string `toml:"cluster"` // имя кластера из конфига
	Base    string `toml:"base"`    // имя базы 1С
	User    string `toml:"user"`
	Pwd     string `toml:"pwd"`
}

// Settings — настройки (сохраняются в ~/.config/lazy1c/settings.toml).
type Settings struct {
	HideRAS      bool           `toml:"hide_ras_sessions"`      // служебные RAS-сеансы
	HideConsole  bool           `toml:"hide_console_sessions"`  // толстый клиент (1CV8)
	HideDesigner bool           `toml:"hide_designer_sessions"` // конфигуратор
	HideIdle     bool           `toml:"hide_idle_sessions"`     // спящие сеансы (клавиша z)
	HideJobs     bool           `toml:"hide_job_sessions"`      // регламентные/фоновые задания
	ReadOnly     bool           `toml:"read_only"`              // спец-режим: только просмотр
	ShowFlags    bool           `toml:"show_base_flags"`        // бейджи РЗ/Вход у баз в дереве
	IB           []IBCred       `toml:"ib"`                     // креды баз (кластер → база)
	Clusters     []ExtraCluster `toml:"cluster"`                // кластеры, добавленные из UI (+)
	Removed      []string       `toml:"removed_clusters"`       // адреса конфиг-кластеров, убранных через D/Delete
	Paused       []string       `toml:"paused_clusters"`        // адреса кластеров на паузе (s) — не опрашивать
	TreeClosed   []string       `toml:"tree_collapsed"`         // адреса свёрнутых кластеров (остальные раскрыты)
	TreeIbOpen   []string       `toml:"tree_bases_open"`        // «кластер|база» открытых баз (новые — закрыты)
}

// isRemovedCluster — убран ли конфиг-кластер из списка пользователем.
func (s *Settings) isRemovedCluster(addr string) bool {
	for _, a := range s.Removed {
		if a == addr {
			return true
		}
	}
	return false
}

// inList — есть ли строка в списке.
func inList(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// markTreeDirty — пометить дерево как изменённое (запись — при следующем тике).
func (m *model) markTreeDirty() { m.treeDirty = true }

// flushTreeState — записать состояние дерева на диск (вызывается с тика).
func (m *model) flushTreeState() {
	if !m.treeDirty {
		return
	}
	m.treeDirty = false
	m.saveTreeStateNow()
}

// saveTreeStateNow — записать состояние дерева (что свёрнуто) в settings.
// Храним только закрытое: новые кластеры и базы появляются раскрытыми —
// «с поправкой на новые данные».
func (m *model) saveTreeStateNow() {
	var closed, ibOpen []string
	for i := range m.state {
		st := &m.state[i]
		if !st.expanded {
			closed = append(closed, st.address)
		}
		for uuid, open := range st.ibOpen {
			if open {
				ibOpen = append(ibOpen, st.address+"|"+uuid)
			}
		}
	}
	m.settings.TreeClosed = closed
	m.settings.TreeIbOpen = ibOpen
	_ = m.settings.save()
}

// applyTreeState — восстановить состояние дерева при старте
// (свёрнутые кластеры; базы — на первом снапшоте, см. restoreIbState).
func (m *model) applyTreeState() {
	for i := range m.state {
		if inList(m.settings.TreeClosed, m.state[i].address) {
			m.state[i].expanded = false
		} else {
			m.state[i].expanded = true // не свёрнут — раскрыть
		}
	}
}

// restoreIbState — первый снапшот кластера: открыть только базы,
// которые пользователь явно раскрыл в прошлый раз. Новые — закрыты.
func (m *model) restoreIbState(st *clusterState) {
	for _, ib := range st.snap.Infobases[st.cluster.GetUuid()] {
		if inList(m.settings.TreeIbOpen, st.address+"|"+ib.GetUuid()) {
			st.ibOpen[ib.GetUuid()] = true
		}
	}
}

// isPausedCluster — стоит ли кластер на паузе (переживает перезапуск).
func (s *Settings) isPausedCluster(addr string) bool {
	for _, a := range s.Paused {
		if a == addr {
			return true
		}
	}
	return false
}

// setPausedCluster — поставить/снять паузу (пишется сразу).
func (s *Settings) setPausedCluster(addr string, paused bool) {
	kept := s.Paused[:0]
	for _, a := range s.Paused {
		if a != addr {
			kept = append(kept, a)
		}
	}
	s.Paused = kept
	if paused {
		s.Paused = append(s.Paused, addr)
	}
}

// unremoveCluster — вернуть кластер в список (при повторном добавлении адреса).
func (s *Settings) unremoveCluster(addr string) {
	kept := s.Removed[:0]
	for _, a := range s.Removed {
		if a != addr {
			kept = append(kept, a)
		}
	}
	s.Removed = kept
}

// ExtraCluster — кластер, добавленный через «+» (хранится в settings.toml
// рядом с ручными из lazy1c.toml; дубли по адресу отфильтровываются).
type ExtraCluster struct {
	Name    string `toml:"name"`
	Address string `toml:"address"`
}

// ibCred — сохранённые креды базы (по имени кластера и базы).
func (s *Settings) ibCred(cluster, base string) (IBCred, bool) {
	for _, c := range s.IB {
		if c.Cluster == cluster && strings.EqualFold(c.Base, base) {
			return c, true
		}
	}
	return IBCred{}, false
}

// ibDefault — единый дефолт кредов ИБ (на все кластеры).
func (s *Settings) ibDefault() (IBCred, bool) {
	return s.ibCred("*", "*")
}

// setIBDefault — записать дефолт (пустые user/pwd — удалить).
func (s *Settings) setIBDefault(user, pwd string) {
	if user == "" && pwd == "" {
		s.removeIBCred("*", "*")
		return
	}
	s.setIBCred(IBCred{Cluster: "*", Base: "*", User: user, Pwd: pwd})
}

// removeIBCred — удалить креды (базы или дефолт).
func (s *Settings) removeIBCred(cluster, base string) {
	out := s.IB[:0]
	for _, c := range s.IB {
		if c.Cluster == cluster && strings.EqualFold(c.Base, base) {
			continue
		}
		out = append(out, c)
	}
	s.IB = out
}

// setIBCred — записать/обновить креды базы.
func (s *Settings) setIBCred(c IBCred) {
	for i := range s.IB {
		if s.IB[i].Cluster == c.Cluster && strings.EqualFold(s.IB[i].Base, c.Base) {
			s.IB[i] = c
			return
		}
	}
	s.IB = append(s.IB, c)
}

func defaultSettings() Settings {
	return Settings{HideRAS: true, HideIdle: true, ShowFlags: true}
}

// settingsPath — ~/.config/lazy1c/settings.toml.
func settingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "lazy1c", "settings.toml")
}

// legacySettingsPath — настройки до переименования проекта (1cras);
// читаются один раз для миграции, чтобы дерево/креды не потерялись.
func legacySettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "1cras", "settings.toml")
}

// loadSettings читает настройки, отсутствующие поля — из дефолтов.
// Договорились: пустая настройка (файла ещё нет, первый запуск) — строго
// read-only; после первого сохранения режим управляется как обычно.
func loadSettings() Settings {
	s := defaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		// миграция со старого пути (проект переименован 1cras → lazy1c)
		if old, oerr := os.ReadFile(legacySettingsPath()); oerr == nil {
			_ = toml.Unmarshal(old, &s)
			_ = s.save() // сразу переложить на новый путь
			return s
		}
		s.ReadOnly = true // первый запуск — безопасный
		return s
	}
	_ = toml.Unmarshal(data, &s)
	return s
}

// save пишет настройки в файл.
func (s Settings) save() error {
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// menuState — меню (x: настройки, B: массовые операции); всё выбираемо.
// Пробел правит в памяти, enter пишет на диск, esc откатывает к снимку.
type menuState struct {
	kind      string   // "settings" | "bulk"
	tab       int      // 0 — Настройки, 1 — Креды (только для settings)
	scroll    int      // прокрутка тела, если меню выше экрана
	cursor    int      // индекс в выбираемых пунктах
	orig      Settings // снимок на момент открытия: esc возвращает всё назад
	rect      rect     // координаты окна меню на экране (для кликов мышью)
	lineEntry []int    // строка экрана меню → индекс пункта (-1 = декорация)
	notice    string   // сообщение внутри меню (например, о блокировке)
}

// rect — прямоугольник окна на экране.
type rect struct{ x, y, w, h int }

// menuEntry — пункт меню: чекбокс (val), команда (key/run) или декорация.
type menuEntry struct {
	label string
	val   *bool  // чекбокс настройки
	roOK  bool   // можно менять даже в read-only (сам переключатель режима)
	key   rune   // команда: выполняется как нажатие клавиши
	fkey  rune   // функциональная клавиша (F5 и т.п.)
	run   func() // команда посложнее (enter-действие)
}

// menuEntries — содержимое меню по его типу и вкладке.
func (m *model) menuEntries(kind string, tab int) []menuEntry {
	if kind == "bulk" {
		return m.bulkMenuEntries()
	}
	if tab == 1 {
		// вкладка «Креды»: единый дефолт ИБ + креды баз (прячем от лишних глаз)
		entries := []menuEntry{{label: "creds"}}
		defUser := "— не задан"
		if d, ok := m.settings.ibDefault(); ok {
			defUser = d.User
		}
		entries = append(entries, menuEntry{
			label: "Дефолт ИБ (все кластеры):  " + defUser,
			run:   func() { m.openCredsForm("", "") },
		})
		for _, c := range m.settings.IB {
			if c.Base == "*" || c.Cluster == "*" {
				continue
			}
			clusterID, baseName := c.Cluster, c.Base
			entries = append(entries, menuEntry{
				label: fmt.Sprintf("база %-14s %-10s %s", baseName+":", clusterID+",", c.User),
				run:   func() { m.openCredsForm(clusterID, baseName) },
			})
		}
		return entries
	}
	// вкладка «Настройки»: показать — наверх, скрытия — следом, остальное — ниже
	entries := []menuEntry{
		{label: "settings"}, // заголовок секции
		{label: "Показывать РЗ/Вход у баз в дереве (v)", val: &m.settings.ShowFlags},
		{label: "Скрывать RAS-сеансы", val: &m.settings.HideRAS},
		{label: "Скрывать сеансы консоли (1CV8)", val: &m.settings.HideConsole},
		{label: "Скрывать сеансы конфигуратора", val: &m.settings.HideDesigner},
		{label: "Скрывать спящие сеансы (z)", val: &m.settings.HideIdle},
		{label: "Скрывать регламентные задания", val: &m.settings.HideJobs},
		{label: "Режим read-only (только просмотр)", val: &m.settings.ReadOnly, roOK: true},
		{label: ""},
	}
	entries = append(entries,
		menuEntry{label: ""},
		menuEntry{label: "keys"}, // заголовок секции
		menuEntry{label: "пробел отметить сеанс (база — все, кластер — пауза)", run: func() {
			m.simulateKey(' ')
		}},
		menuEntry{label: "d     завершить сеанс / все отмеченные", key: 'd'},
		menuEntry{label: "esc   снять все отметки", run: func() {
			m.simulateKey(0x1B)
		}},
		menuEntry{label: "B     массовые операции (по отметкам)", run: func() {
			m.simulateKey('B')
		}},
		menuEntry{label: "r     запрет регл. заданий (на базе)", key: 'r'},
		menuEntry{label: "b     блокировка сеансов (на базе)", key: 'b'},
		menuEntry{label: "enter открыть базу на весь экран", run: func() {
			if r := m.curRow(); r != nil && r.kind == rowInfobase {
				m.zoom = &zoomState{clusterID: r.clusterID, ib: r.ib}
			}
		}},
		menuEntry{label: "[     предыдущая вкладка", key: '['},
		menuEntry{label: "]     следующая вкладка", key: ']'},
		menuEntry{label: "/     фильтр", key: '/'},
		menuEntry{label: "z     спящие сеансы", key: 'z'},
		menuEntry{label: "a     добавить кластер (в форме — вставка адрес:порт)", run: func() {
			m.simulateKey('a')
		}},
		menuEntry{label: "D     убрать кластер из мониторинга", run: func() {
			m.simulateKey('D')
		}},
		menuEntry{label: "e     реквизиты кластера (на кластере)", run: func() {
			m.simulateKey('e')
		}},
		menuEntry{label: "s     вкл/выкл опрос кластера", key: 's'},
		menuEntry{label: "F5    обновить", fkey: tea.KeyF5},
		menuEntry{label: "1/2   фокус панели", key: '1'},
		menuEntry{label: "q     выход", key: 'q'},
	)
	return entries
}

// bulkMenuEntries — массовые операции над ОТМЕЧЕННЫМИ пробелом сеансами (✓);
// без отметок меню открывается с предупреждением и ничего не делает.
func (m *model) bulkMenuEntries() []menuEntry {
	n := len(m.marked)
	return []menuEntry{
		{label: "bulk-title"},
		{label: fmt.Sprintf("Завершить отмеченные сеансы (%d)", n), run: func() {
			m.beginTerminateMarked()
		}},
		{label: "Снять все отметки", run: func() {
			if n == 0 {
				m.pushLog("отметок и так нет")
				return
			}
			m.marked = map[string]string{}
			m.pushLog("отметки сняты")
		}},
	}
}




// selectableEntries — индексы выбираемых пунктов (чекбоксы и команды).
func (m *model) selectableEntries(kind string, tab int) []int {
	var out []int
	for i, e := range m.menuEntries(kind, tab) {
		if e.val != nil || e.key != 0 || e.fkey != 0 || e.run != nil {
			out = append(out, i)
		}
	}
	return out
}

// sessionHidden — скрыт ли сеанс настройками видимости категорий.
func (m *model) sessionHidden(appID string) bool {
	switch appID {
	case "1CV8", "SrvrConsole": // толстый клиент и сеансы MMC-консоли — одна категория
		return m.settings.HideConsole
	case "Designer":
		return m.settings.HideDesigner
	}
	return false
}

// isReadOnly — спец-режим: флаг/конфиг или переключатель из меню.
func (m *model) isReadOnly() bool { return m.cfg.ReadOnly || m.settings.ReadOnly }

// denyMutations — проверить и залогировать запрет изменения.
func (m *model) denyMutations(what string) bool {
	if !m.isReadOnly() {
		return false
	}
	reason := "read-only"
	if m.cfg.ReadOnly {
		reason = "read-only (запущен с -read-only/конфига)"
	}
	m.pushLog(reason + ": " + what + " — запрещено")
	return true
}

// renderMenu — оверлей-меню: возвращает текст и строку→пункт для кликов.
// Высота окна фиксирована для обоих табов (не прыгает при переключении);
// если содержимое выше экрана — окно прокручивается (scroll, колесо, PgUp/PgDn).
func (m *model) renderMenu() (string, []int) {
	kind := ""
	if m.menu != nil {
		kind = m.menu.kind
	}
	tab := 0
	if m.menu != nil {
		tab = m.menu.tab
	}
	lines, lineEntry := m.menuBody(kind, tab)
	// фиксированная высота: максимум по обоим табам
	height := len(lines)
	if kind == "settings" {
		other, _ := m.menuBody(kind, 1-tab)
		if len(other) > height {
			height = len(other)
		}
	}
	vis := m.menuViewport(height)
	if m.menu != nil {
		if m.menu.scroll > height-vis {
			m.menu.scroll = height - vis
		}
		if m.menu.scroll < 0 {
			m.menu.scroll = 0
		}
		scr := m.menu.scroll
		if scr > len(lines) {
			scr = len(lines)
		}
		end := scr + vis
		if end > len(lines) {
			end = len(lines)
		}
		lines = lines[scr:end]
		lineEntry = lineEntry[scr:end]
	}
	for len(lines) < vis {
		lines = append(lines, "")
		lineEntry = append(lineEntry, -1)
	}
	return m.menuWrap(kind, tab, lines), lineEntry
}

// menuViewport — сколько строк тела видно (экран ограничивает).
func (m *model) menuViewport(height int) int {
	vis := height
	if max := m.height - 8; vis > max { // рамки, паддинги модалки, воздух
		vis = max
	}
	if vis < 3 {
		vis = 3
	}
	return vis
}

// menuWrap — обернуть тело в модалку с шапкой (табы/число отметок).
func (m *model) menuWrap(kind string, tab int, lines []string) string {
	boxTitle := " Menu "
	if kind == "bulk" {
		boxTitle = fmt.Sprintf(" Menu · отмечено %d ", len(m.marked))
	}
	if kind == "settings" {
		// таб-бар в шапке меню — единый стиль с панелями приложения
		var parts []string
		for i, t := range []string{"Настройки", "Креды"} {
			if i == tab {
				parts = append(parts, styleTabActive.Render(t))
			} else {
				parts = append(parts, styleVal.Render(t))
			}
		}
		boxTitle = " " + strings.Join(parts, styleKey.Render(" - ")) + " "
	}
	return withTitle(styleModal.Width(64).Render(strings.Join(lines, "\n")), boxTitle)
}

// menuBody — строки тела меню для типа/таба (без модалки).
func (m *model) menuBody(kind string, tab int) ([]string, []int) {
	entries := m.menuEntries(kind, tab)
	sel := m.selectableEntries(kind, tab)
	cursorEntry := -1
	if m.menu != nil && m.menu.cursor < len(sel) {
		cursorEntry = sel[m.menu.cursor]
	}
	var lines []string
	var lineEntry []int
	add := func(s string, e int) {
		lines = append(lines, s)
		lineEntry = append(lineEntry, e)
	}
	// заголовок тела: у bulk — «Массовые операции», у таба кредов — предупреждение
	bodyTitle := ""
	if m.menu != nil && m.menu.kind == "bulk" {
		bodyTitle = " Массовые операции"
	} else if m.menu != nil && m.menu.kind == "settings" && m.menu.tab == 1 {
		bodyTitle = " Креды ИБ (хранятся в открытом виде)"
	}
	// заголовок тела: у bulk — «Массовые операции», у таба кредов — предупреждение
	if bodyTitle != "" {
		add(styleTitle.Render(bodyTitle), -1)
	}
	for i, it := range entries {
		switch {
		case it.label == "settings":
			add(styleTitle.Render(" Настройки"), -1)
		case it.label == "creds":
			// заголовок тела уже выведен через bodyTitle
		case it.label == "bulk-title":
			// заголовок уже выведен
		case it.label == "keys":
			add("", -1)
			add(styleTitle.Render(" Клавиши"), -1)
		case it.val != nil:
			box := " "
			if *it.val {
				box = "✓"
			}
			note := ""
			if it.roOK && m.cfg.ReadOnly {
				note = styleWarn.Render("  (задан флагом)")
			}
			line := fmt.Sprintf("  [%s] %s%s", box, styleVal.Render(it.label), note)
			if i == cursorEntry {
				line = styleSelected.Render(line)
			}
			add(line, i)
		case it.label == "":
			add("", -1)
		default:
			// bulk-действия — красным (опасные), справочные клавиши — тускло
			line := "  " + styleHint.Render(it.label)
			if kind == "bulk" {
				line = "  " + styleErr.Render(it.label)
			}
			if i == cursorEntry {
				line = styleSelected.Render("  " + it.label)
			}
			add(line, i)
		}
	}
	add("", -1)
	if m.menu != nil && m.menu.notice != "" {
		add(styleWarn.Render(" "+m.menu.notice), -1)
	}
	// подсказки кнопок — только в статус-баре, не дублируем в теле меню
	return lines, lineEntry
}

// menuToggle — переключить чекбокс в памяти (запись — по enter).
// Read-only не мешает: настройки видимости — локальные, кластер не трогают.
// Блокируется только сам пункт read-only, когда режим задан флагом.
func (m *model) menuToggle(it menuEntry) {
	if it.val == nil {
		return
	}
	if it.roOK && m.cfg.ReadOnly {
		m.menuNotice("read-only задан флагом/конфигом — пункт неактивен")
		return
	}
	*it.val = !*it.val
	m.pushLog("настройки (не записано): " + it.label + " = " + onOff(*it.val))
	m.rebuild()
}

// menuClampScroll — удержать прокрутку в границах содержимого.
func (m *model) menuClampScroll() {
	if m.menu == nil {
		return
	}
	lines, _ := m.menuBody(m.menu.kind, m.menu.tab)
	height := len(lines)
	if m.menu.kind == "settings" {
		other, _ := m.menuBody(m.menu.kind, 1-m.menu.tab)
		if len(other) > height {
			height = len(other)
		}
	}
	vis := m.menuViewport(height)
	if m.menu.scroll > height-vis {
		m.menu.scroll = height - vis
	}
	if m.menu.scroll < 0 {
		m.menu.scroll = 0
	}
}

// menuFollowCursor — автопрокрутка: выбранный пункт всегда в окне.
func (m *model) menuFollowCursor() {
	if m.menu == nil {
		return
	}
	sel := m.selectableEntries(m.menu.kind, m.menu.tab)
	if m.menu.cursor >= len(sel) {
		return
	}
	want := sel[m.menu.cursor]
	_, lineEntry := m.menuBody(m.menu.kind, m.menu.tab)
	line := -1
	for i, e := range lineEntry {
		if e == want {
			line = i
			break
		}
	}
	if line < 0 {
		return
	}
	lines, _ := m.menuBody(m.menu.kind, m.menu.tab)
	height := len(lines)
	if m.menu.kind == "settings" {
		other, _ := m.menuBody(m.menu.kind, 1-m.menu.tab)
		if len(other) > height {
			height = len(other)
		}
	}
	vis := m.menuViewport(height)
	if line < m.menu.scroll {
		m.menu.scroll = line
	}
	if line >= m.menu.scroll+vis {
		m.menu.scroll = line - vis + 1
	}
}

// menuSave — записать настройки на диск (enter).
func (m *model) menuSave() {
	if err := m.settings.save(); err != nil {
		m.pushLog("настройки не сохранены: " + err.Error())
		m.menuNotice("не сохранено: " + err.Error())
		return
	}
	m.pushLog("настройки записаны: " + settingsPath())
}

// menuNotice — короткое сообщение внутри меню (почему действие не выполнено).
func (m *model) menuNotice(s string) {
	if m.menu != nil {
		m.menu.notice = s
		m.pushLog(s)
	}
}

// menuRunEntry — выполнить пункт: чекбокс переключается, команда — действует.
func (m *model) menuRunEntry(kind string, tab, idx int) {
	entries := m.menuEntries(kind, tab)
	if idx < 0 || idx >= len(entries) {
		return
	}
	it := entries[idx]
	if it.val != nil {
		m.menuToggle(it)
		return
	}
	if it.run != nil {
		it.run()
		return
	}
	if it.key != 0 {
		m.simulateKey(it.key)
		return
	}
	if it.fkey != 0 {
		m2, _ := m.updateKeys(tea.KeyPressMsg{Code: it.fkey})
		_ = m2
	}
}

// updateMenu — клавиши открытого меню.
// enter — применить и закрыть, esc — отменить изменения, пробел — чекбокс.
func (m *model) updateMenu(key string) {
	key = keyNorm(key)
	sel := m.selectableEntries(m.menu.kind, m.menu.tab)
	if len(sel) == 0 {
		return
	}
	switch key {
	case "j", "down":
		if m.menu.cursor < len(sel)-1 {
			m.menu.cursor++
		}
		m.menuFollowCursor()
	case "k", "up":
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		m.menuFollowCursor()
	case "pgdown":
		m.menu.scroll += 3
		m.menuClampScroll()
	case "pgup":
		m.menu.scroll -= 3
		m.menuClampScroll()
	case " ", "space":
		if m.menu.kind == "bulk" {
			// в bulk-меню пробел выполняет действие как enter
			idx := sel[m.menu.cursor]
			kind := m.menu.kind
			m.menu = nil
			m.menuRunEntry(kind, 0, idx)
			return
		}
		m.menuToggle(m.menuEntries(m.menu.kind, m.menu.tab)[sel[m.menu.cursor]])
	case "enter":
		// записать и закрыть; команду под курсором — выполнить
		idx := sel[m.menu.cursor]
		kind, tab := m.menu.kind, m.menu.tab
		entries := m.menuEntries(kind, tab)
		if entries[idx].val != nil {
			m.menuSave()
			m.menu = nil
			return
		}
		m.menu = nil
		m.menuRunEntry(kind, tab, idx)
	case "[", "]":
		if m.menu.kind == "settings" {
			m.menu.tab = 1 - m.menu.tab
			m.menu.cursor = 0
			m.menu.notice = ""
		}
	case "esc":
		// отмена: вернуть всё к снимку на момент открытия.
		// Снимок есть только у меню настроек; у bulk-меню его нет,
		// иначе откат затирал бы настройки пустым значением (баг).
		if m.menu.kind == "settings" {
			m.settings = m.menu.orig
		}
		m.menu = nil
		m.rebuild()
	case "q":
		m.menu = nil
	}
}
