package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// rowKind — тип строки дерева.
type rowKind int

const (
	rowCluster rowKind = iota
	rowInfobase
	rowSession
	rowSvc        // служебный раздел
	rowNoSessions // заглушка у раскрытой базы без сеансов
)

// row — плоская строка дерева с ссылкой на объект.
type row struct {
	kind      rowKind
	clusterID string // индекс в m.clusters
	depth     int

	cluster *serializev1.ClusterInfo
	ib      *serializev1.InfobaseSummaryInfo
	session *serializev1.SessionInfo
}

// isServiceSession определяет служебный сеанс кластера (не человек, не регл. задание).
// Управляется настройкой «Скрывать RAS-сеансы».
func isServiceSession(appID string) bool {
	switch appID {
	case "RAS", "JobScheduler", "AgentStandardCall", "COMConnector":
		return true
	}
	return false
}

// sessionShown — видимость сеанса везде (дерево, zoom, счётчики):
// настройки категорий + спящие + регламентные. Текстовый фильтр тут ни при чём.
func (m *model) sessionShown(s *serializev1.SessionInfo) bool {
	if m.sessionHidden(s.GetAppId()) {
		return false
	}
	if isServiceSession(s.GetAppId()) && m.settings.HideRAS {
		return false
	}
	if m.settings.HideIdle && s.GetHibernate() {
		return false
	}
	if m.settings.HideJobs && isJobSession(s.GetAppId()) {
		return false
	}
	return true
}

// countShownSessions — сколько сеансов кластера реально видно (для строки кластера).
func (m *model) countShownSessions(sessions []*serializev1.SessionInfo) int {
	n := 0
	for _, s := range sessions {
		if m.sessionShown(s) {
			n++
		}
	}
	return n
}

// filterMatch — проходит ли строка под фильтр (по подстроке, без регистра).
func (m *model) filterMatch(fields ...string) bool {
	n := strings.ToLower(m.filter)
	if n == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), n) {
			return true
		}
	}
	return false
}

// filterClusters — фильтр по именам кластеров (scope=0).
func (m *model) filterClusters(st *clusterState) bool {
	if m.filter == "" || m.filterScope != 0 {
		return true
	}
	return m.filterMatch(st.id, st.address, st.cluster.GetHost())
}

// filterBases — фильтр по именам баз (scope=1).
func (m *model) filterBases(ib *serializev1.InfobaseSummaryInfo) bool {
	if m.filter == "" || m.filterScope != 1 {
		return true
	}
	return m.filterMatch(ib.GetName(), ib.GetDescr())
}

// filterSessions — фильтр по атрибутам сеансов (scope=2).
func (m *model) filterSessions(s *serializev1.SessionInfo) bool {
	if m.filter == "" || m.filterScope != 2 {
		return true
	}
	return m.filterMatch(s.GetUserName(), s.GetAppId(), s.GetHost())
}

// clusterHasFilterHits — есть ли в кластере базы или сеансы, проходящие фильтр
// (scope=1 — имя базы, scope=2 — атрибуты сеансов).
func (m *model) clusterHasFilterHits(st *clusterState) bool {
	cuuid := st.cluster.GetUuid()
	for _, ib := range st.snap.Infobases[cuuid] {
		if m.filterScope == 1 && m.filterBases(ib) {
			return true
		}
		if m.filterScope == 2 {
			for _, s := range st.snap.Sessions[cuuid] {
				if s.GetInfobaseId() == ib.GetUuid() && m.sessionVisible(s) {
					return true
				}
			}
		}
	}
	return false
}

// sessionVisible — показывать ли сеанс в дереве (видимость + фильтр сеансов).
func (m *model) sessionVisible(s *serializev1.SessionInfo) bool {
	return m.sessionShown(s) && m.filterSessions(s)
}

// countUsersJobs — счётчики базы с учётом настроек скрытия:
// 👤 — число сеансов живых людей (тонкий/толстый/веб/конфигуратор,
// один пользователь в двух клиентах — это 2 сеанса),
// ⚙ — регламентные сеансы. Скрытые категории не считаются нигде.
func (m *model) countUsersJobs(sessions []*serializev1.SessionInfo, infobaseID string) (users, jobs int) {
	for _, s := range sessions {
		if s.GetInfobaseId() != infobaseID {
			continue
		}
		if m.sessionHidden(s.GetAppId()) ||
			(isServiceSession(s.GetAppId()) && m.settings.HideRAS) {
			continue
		}
		if isJobSession(s.GetAppId()) {
			jobs++
		} else {
			users++
		}
	}
	return users, jobs
}

// buildRows собирает видимые строки дерева из снапшотов.
func (m *model) buildRows() []row {
	var rows []row
	for ci := range m.state {
		st := &m.state[ci]
		// scope=0: кластеры фильтруются по имени
		if m.filter != "" && m.filterScope == 0 && !m.filterClusters(st) {
			continue
		}
		// scope=1/2: кластер показывается только если есть совпавшие базы/сеансы
		if m.filter != "" && m.filterScope > 0 && st.snap != nil && st.cluster != nil {
			if !m.clusterHasFilterHits(st) {
				continue
			}
		}
		rows = append(rows, row{kind: rowCluster, clusterID: st.id, cluster: st.cluster, depth: 0})
		if st.snap == nil || !st.expanded {
			continue
		}
		cuuid := st.cluster.GetUuid()
		ibList := st.snap.Infobases[cuuid]
		if len(ibList) == 0 {
			rows = append(rows, row{kind: rowSvc, clusterID: st.id, depth: 1})
			continue
		}
		// стабильный порядок: сортировка по имени, чтобы список не прыгал
		// при обновлениях снапшота (разные движки отдают в разном порядке)
		sorted := make([]*serializev1.InfobaseSummaryInfo, len(ibList))
		copy(sorted, ibList)
		sort.Slice(sorted, func(i, j int) bool {
			return strings.ToLower(sorted[i].GetName()) < strings.ToLower(sorted[j].GetName())
		})
		for _, ib := range sorted {
			// фильтр по уровню: базы фильтруются только при scope=1 (по имени)
			// или scope=2 (по сеансам внутри)
			ibMatch := m.filterBases(ib)
			var visible []*serializev1.SessionInfo
			for _, s := range st.snap.Sessions[cuuid] {
				if s.GetInfobaseId() != ib.GetUuid() || !m.sessionVisible(s) {
					continue
				}
				visible = append(visible, s)
			}
			// scope=2: база видима если есть сеансы, проходящие фильтр
			// (кластер на паузе — фильтр сеансов не применяем, данные заморожены)
			if m.filter != "" && m.filterScope == 2 && st.enabled && len(visible) == 0 {
				continue
			}
			// scope=1: база видима если имя проходит фильтр
			if m.filter != "" && m.filterScope == 1 && !ibMatch {
				continue
			}
			// scope=0: базы не фильтруются (фильтр на кластерах выше)
			if !ibMatch && m.filterScope != 0 && len(visible) == 0 && m.filter != "" {
				continue
			}
			// Shift+→ уровень 1: показать только базы с сеансами
			if st.expandOnlyActive && m.filter == "" && len(visible) == 0 {
				continue
			}
			rows = append(rows, row{kind: rowInfobase, clusterID: st.id, cluster: st.cluster, ib: ib, depth: 1})
			open := st.ibOpen[ib.GetUuid()]
			if !open {
				continue
			}
			if len(visible) == 0 {
				// раскрыли базу без сеансов — показываем заглушку,
				// чтобы действие раскрытия всегда имело видимый отклик
				rows = append(rows, row{kind: rowNoSessions, clusterID: st.id, cluster: st.cluster, ib: ib, depth: 2})
				continue
			}
			for _, s := range visible {
				rows = append(rows, row{kind: rowSession, clusterID: st.id, cluster: st.cluster, session: s, depth: 2})
			}
		}
		// сеансы без известной базы
		for _, s := range st.snap.Sessions[cuuid] {
			known := false
			for _, ib := range sorted {
				if s.GetInfobaseId() == ib.GetUuid() {
					known = true
					break
				}
			}
			if !known && m.sessionVisible(s) {
				rows = append(rows, row{kind: rowSession, clusterID: st.id, cluster: st.cluster, session: s, depth: 2})
			}
		}
	}
	return rows
}

// renderTree рисует дерево шириной w и высотой h.
func (m *model) renderTree(w, h int) string {
	rows := m.rows
	out := make([]string, 0, h)
	if len(m.state) == 0 {
		// пустое дерево: подсказка по центру панели
		empty := "нет кластеров"
		hint := "a: добавить"
		pad := (w - 4 - len([]rune(empty))) / 2
		if pad < 0 {
			pad = 0
		}
		spaces := strings.Repeat(" ", pad)
		out = append(out, spaces+styleHibernat.Render(empty),
			spaces+styleHint.Render(hint))
	}
	// рамка занимает 2 строки — контент не больше h-2
	maxRows := h - 2
	if maxRows < 1 {
		maxRows = 1
	}
	top := m.treeScroll
	bottom := min(top+maxRows, len(rows))
	for i := top; i < bottom; i++ {
		r := rows[i]
		styled, plain := m.rowText(&r)
		if i == m.cursor {
			// курсор: однотонная плашка на ВСЮ строку панели, от левого края до правого.
			// Ширина по ячейкам (ansi.StringWidth): 👤/⚙ занимают 2 клетки,
			// подсчёт рунами урезал бы плашку.
			fill := (w - 2) - ansi.StringWidth(plain)
			if fill < 0 {
				fill = 0
			}
			styled = styleSelected.Render(plain + strings.Repeat(" ", fill))
		}
		// обрезка по ширине панели — без переноса
		styled = ansi.Truncate(styled, w-2, "…")
		out = append(out, styled)
	}
	for len(out) < maxRows {
		out = append(out, "")
	}
	border := styleBorderInactive
	if m.focus == 0 {
		border = styleBorderActive
	}
	// шапка: номер панели (фокус 1) + Кластеры + бейджи
	head := " [1] Кластеры"
	// активные фильтры видимости — краткими бейджами (могут быть все сразу)
	var badges []string
	add := func(label string) {
		badges = append(badges, styleErr.Render("NO")+" "+styleHint.Render(label))
	}
	if m.settings.HideConsole {
		add("MMC")
	}
	if m.settings.HideRAS {
		add("RAS")
	}
	if m.settings.HideDesigner {
		add("conf")
	}
	if m.settings.HideIdle {
		add("Zzz")
	}
	if m.settings.HideJobs {
		add("РЗ")
	}
	// правый блок шапки: фильтры + read-only, к правому краю панели
	// (5 колонок оформления withTitle)
	if m.isReadOnly() {
		badges = append(badges, styleHint.Render("read-only"))
	}
	if len(badges) > 0 {
		bl := strings.Join(badges, "  ")
		// w-6: withTitle режет заголовок по w-6 (5 оформления + запас)
		pad := w - 6 - ansi.StringWidth(head) - ansi.StringWidth(bl) - 1 // −1: хвостовой пробел head+" "
		if pad < 1 {
			pad = 1
		}
		head += strings.Repeat(" ", pad) + bl
	}
	return withTitle(border.Width(w).Height(h).Render(joinLines(out)), head+" ")
}

// rowText возвращает строку дерева в двух видах: цветную и чистую (для курсора).
func (m *model) rowText(r *row) (styled, plain string) {
	switch r.kind {
	case rowCluster:
		st := m.stateByName(r.clusterID)
		name := r.cluster.GetName()
		if name == "" {
			name = st.id
		}
		arrow := "▸"
		if st.expanded {
			arrow = "▾"
		}
		switch {
		case !st.enabled:
			// пауза: показываем последнее известное состояние (замороженный снапшот)
			if r.cluster != nil {
				host := r.cluster.GetHost()
				if host == "" {
					host = hostOf(st.address)
				}
				name = fmt.Sprintf("%s:%d", host, r.cluster.GetPort())
				if st.version != "" {
					name += " · " + st.version
				}
				if st.snap != nil {
					cuuid := r.cluster.GetUuid()
					users2, jobs2 := 0, 0
					for _, ss := range st.snap.Sessions[cuuid] {
						if !m.sessionShown(ss) {
							continue
						}
						if isJobSession(ss.GetAppId()) {
							jobs2++
						} else {
							users2++
						}
					}
					if users2 > 0 {
						name += fmt.Sprintf("  👤%d", users2)
					}
					if jobs2 > 0 {
						name += fmt.Sprintf("  ⚙%d", jobs2)
					}
				}
			}
			return fmt.Sprintf("%s %s %s", arrow, styleHibernat.Render("⏸"), styleHibernat.Render(name)),
				fmt.Sprintf("%s ⏸ %s", arrow, name)
		case st.snap != nil:
			cuuid := r.cluster.GetUuid()
			host := r.cluster.GetHost()
			if host == "" {
				host = hostOf(st.address)
			}
			name = fmt.Sprintf("%s:%d", host, r.cluster.GetPort())
			versionStyled, versionPlain := "", ""
			if st.version != "" {
				versionStyled = styleHint.Render(" · " + st.version)
				versionPlain = " · " + st.version
			}
			// 👤 людей (сеансов) и ⚙ регламентных — как у баз
			users, jobs := m.countUsersJobs(st.snap.Sessions[cuuid], "")
			// countUsersJobs фильтрует по infobaseID — для всего кластера считаем по-другому
			users, jobs = 0, 0
			for _, ss := range st.snap.Sessions[cuuid] {
				if !m.sessionShown(ss) {
					continue
				}
				if isJobSession(ss.GetAppId()) {
					jobs++
				} else {
					users++
				}
			}
			counts, countsPlain := "", ""
			if users > 0 {
				counts += "  " + styleOnline.Render(fmt.Sprintf("👤%d", users))
				countsPlain += fmt.Sprintf("  👤%d", users)
			}
			if jobs > 0 {
				counts += "  " + styleJobs.Render(fmt.Sprintf("⚙%d", jobs))
				countsPlain += fmt.Sprintf("  ⚙%d", jobs)
			}
			ping := stylePing.Render(fmt.Sprintf("  %dms", st.snap.Took.Milliseconds()))
			pingPlain := fmt.Sprintf("  %dms", st.snap.Took.Milliseconds())
			styled := fmt.Sprintf("%s %s %s%s%s%s", arrow, styleOnline.Render("●"),
				styleCluster.Render(name), versionStyled, counts, ping)
			return styled, fmt.Sprintf("%s ● %s%s%s%s", arrow, name, versionPlain, countsPlain, pingPlain)
		default:
			// недоступен, но личность знаем с прошлого подключения:
			// сервер:порт · версия; если ни разу не отвечал — имя из конфига
			if r.cluster != nil {
				host := r.cluster.GetHost()
				if host == "" {
					host = hostOf(st.address)
				}
				name = fmt.Sprintf("%s:%d", host, r.cluster.GetPort())
				if st.version != "" {
					name += " · " + st.version
				}
			}
			return fmt.Sprintf("%s %s %s", arrow, styleOffline.Render("○"), styleCluster.Render(name+" — недоступен")),
				fmt.Sprintf("%s ○ %s", arrow, name+" — недоступен")
		}
	case rowInfobase:
		st := m.stateByName(r.clusterID)
		badge, badgePlain := "", ""
		if st != nil && st.snap != nil {
			users, jobs := m.countUsersJobs(st.snap.Sessions[r.cluster.GetUuid()], r.ib.GetUuid())
			var parts, partsPlain []string
			if users > 0 {
				parts = append(parts, styleOnline.Render(fmt.Sprintf("👤%d", users)))
				partsPlain = append(partsPlain, fmt.Sprintf("👤%d", users))
			}
			if jobs > 0 {
				parts = append(parts, styleJobs.Render(fmt.Sprintf("⚙%d", jobs)))
				partsPlain = append(partsPlain, fmt.Sprintf("⚙%d", jobs))
			}
			if len(parts) > 0 {
				badge = "  " + strings.Join(parts, " ")
				badgePlain = "  " + strings.Join(partsPlain, " ")
			}
		}
		// статусы запретов у всех баз (карточки приходят с каждым опросом);
		// отображение настраивается в меню, по умолчанию включено.
		// вкл — зелёным (как активная рамка), выкл — красным (как exited-контейнеры);
		// подписи — приглушённо, элементы разнесены пробелами
		if m.settings.ShowFlags {
			if e := m.ibInfo[r.clusterID+"/"+r.ib.GetUuid()]; e != nil && e.info != nil {
				onOff := func(denied bool) string {
					if denied {
						return styleErr.Render("выкл")
					}
					return styleOnline.Render("вкл")
				}
				flags := stylePing.Render("РЗ:") + onOff(e.info.GetScheduledJobsDeny()) +
					"   " + stylePing.Render("Вход:") + onOff(e.info.GetSessionsDeny())
				plainJ, plainV := "вкл", "вкл"
				if e.info.GetScheduledJobsDeny() {
					plainJ = "выкл"
				}
				if e.info.GetSessionsDeny() {
					plainV = "выкл"
				}
				badge += "    " + flags
				badgePlain += fmt.Sprintf("    РЗ:%s   Вход:%s", plainJ, plainV)
			}
		}
		// кластер на паузе — базы серым
		ibStyle := styleInfobase
		if st := m.stateByName(r.clusterID); st != nil && !st.enabled {
			ibStyle = styleHibernat
		}
		return fmt.Sprintf("  %s%s", ibStyle.Render(r.ib.GetName()), badge),
			fmt.Sprintf("  %s%s", r.ib.GetName(), badgePlain)
	case rowSession:
		s := r.session
		// сеанс в процессе завершения — серым + живая крутилка
		if m.isPendingKill(s.GetUuid()) {
			user := s.GetUserName()
			if user == "" {
				user = "—"
			}
			plain := fmt.Sprintf("      %s %s@%s  %s завершается", appIDLabel(s.GetAppId()), user, s.GetHost(), m.spinFrame())
			styled := fmt.Sprintf("      %s %s@%s %s",
				styleHibernat.Render(appIDLabel(s.GetAppId())),
				styleHibernat.Render(user), styleHibernat.Render(s.GetHost()),
				styleErr.Render(m.spinFrame()+" завершается"))
			return styled, plain
		}
		user := s.GetUserName()
		if user == "" {
			user = "—"
		}
		// отметка пробелом занимает первые две клетки шестиклеточного отступа
		indent, indentStyled := "      ", "      "
		if _, ok := m.marked[s.GetUuid()]; ok {
			indent, indentStyled = "    ✓ ", "    "+styleMark.Render("✓")+" "
		}
		// имя пользователя — тем же цветом, что счётчики у базы:
		// люди зелёным, регламентные задания жёлтым
		userStyle := styleOnline
		if isJobSession(s.GetAppId()) {
			userStyle = styleJobs
		}
		// акценты: тип клиента — главный (ярко), имя компьютера — второстепенно
		appStyle := styleVal
		hostStyle := styleSession
		if s.GetAppId() == "Designer" {
			appStyle = styleErr // конфигуратор — красным
		}
		// кластер на паузе — всё серым
		if st := m.stateByName(r.clusterID); st != nil && !st.enabled {
			appStyle = styleHibernat
			hostStyle = styleHibernat
			userStyle = styleHibernat
		}
		userStyled := userStyle.Render(user)
		if s.GetHibernate() {
			appStyle = styleHibernat
			hostStyle = styleHibernat
		}
		app := appIDLabel(s.GetAppId())
		plain := fmt.Sprintf("%s%s %s@%s", indent, app, user, s.GetHost())
		styled := fmt.Sprintf("%s%s %s@%s", indentStyled, appStyle.Render(app), userStyled, hostStyle.Render(s.GetHost()))
		if s.GetHibernate() {
			plain += "  (спит)"
			styled += "  " + styleHibernat.Render("(спит)")
		}
		return styled, plain
	case rowNoSessions:
		return styleHibernat.Render("      — нет сеансов —"), "      — нет сеансов —"
	default: // rowSvc
		return styleHibernat.Render("  — нет информационных баз —"), "  — нет информационных баз —"
	}
}

// isJobSession определяет фоновый/регламентный сеанс по app-id.
func isJobSession(appID string) bool {
	a := strings.ToLower(appID)
	return strings.Contains(a, "job") || strings.Contains(a, "фонов")
}

// stateByName ищет состояние кластера по его ID из конфига.
func (m *model) stateByName(id string) *clusterState {
	for i := range m.state {
		if m.state[i].id == id {
			return &m.state[i]
		}
	}
	return nil
}

func joinLines(lines []string) string {
	s := ""
	for i, l := range lines {
		if i > 0 {
			s += "\n"
		}
		s += l
	}
	return s
}
