package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// detailTabs — вкладки панели деталей по типу выбранной строки (как [ ] в lazydocker).
func (m *model) detailTabs() []string {
	r := m.curRow()
	if r == nil {
		return []string{"Справка"}
	}
	switch r.kind {
	case rowCluster:
		return []string{"Сводка", "Сеансы", "Процессы", "Блокировки", "Серверы", "Менеджеры"}
	case rowInfobase:
		return []string{"Сводка", "Свойства", "Блокировки"}
	default:
		return []string{"Сводка"}
	}
}

// renderDetail рисует панель деталей выбранной строки шириной w, высотой h.
// Вкладки — заголовок окна (как в lazydocker: Logs - Stats - Env).
func (m *model) renderDetail(w, h int) string {
	tabs := m.detailTabs()
	if m.detailTab >= len(tabs) {
		m.detailTab = 0
	}
	var b strings.Builder
	r := m.curRow()
	tab := tabs[m.detailTab]
	// таб-бар — всегда (одиночный таб тоже подсвечен как активный); [2] — фокус
	title := " [2] " + m.renderTabBar(tabs) + " "
	if r == nil {
		b.WriteString(styleTitle.Render(" lazy1c — RAS TUI"))
		b.WriteString("\n\n")
		b.WriteString(hintText())
		title = " [2] Справка "
	} else {
		switch {
		case r.kind == rowCluster && tab == "Сеансы":
			m.renderClusterSessionsTab(&b, m.stateByName(r.clusterID))
		case r.kind == rowCluster && tab == "Процессы":
			renderProcessesTab(&b, m.stateByName(r.clusterID))
		case r.kind == rowCluster && tab == "Блокировки":
			renderLocksTab(&b, m.stateByName(r.clusterID))
		case r.kind == rowCluster && tab == "Серверы":
			renderServersTab(&b, m.stateByName(r.clusterID))
		case r.kind == rowCluster && tab == "Менеджеры":
			renderManagersTab(&b, m.stateByName(r.clusterID))
		case r.kind == rowInfobase && tab == "Свойства":
			m.renderInfobaseProps(&b, r)
		case r.kind == rowInfobase && tab == "Блокировки":
			renderInfobaseLocksTab(&b, m.stateByName(r.clusterID), r)
		case r.kind == rowInfobase:
			m.renderInfobaseDetail(&b, r, m.stateByName(r.clusterID))
		case r.kind == rowSession:
			renderSessionDetail(&b, r.session)
		default:
			b.WriteString(styleHibernat.Render(" — нет данных —"))
		}
	}
	body := b.String()
	// прокрутка деталей
	lines := strings.Split(body, "\n")
	top := clamp(m.detailScroll, 0, max(0, len(lines)-h+2))
	if top > 0 {
		lines = lines[top:]
	}
	if len(lines) > h-2 {
		lines = lines[:h-2]
	}
	border := styleBorderInactive
	if m.focus == 1 {
		border = styleBorderActive
	}
	return withTitle(border.Width(w).Height(h).Render(strings.Join(lines, "\n")), title)
}

// renderTabBar — вкладки для заголовка окна: активная — цветом и жирным,
// как в lazydocker; неактивные — приглушённые.
func (m *model) renderTabBar(tabs []string) string {
	var parts []string
	for i, t := range tabs {
		if i == m.detailTab {
			parts = append(parts, styleTabActive.Render(t))
		} else {
			parts = append(parts, styleVal.Render(t))
		}
	}
	return strings.Join(parts, styleKey.Render(" - "))
}

func renderClusterDetail(b *strings.Builder, st *clusterState) {
	if st == nil {
		return
	}
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Кластер"))
	kv(b, "Имя", st.cluster.GetName())
	kv(b, "Агент", fmt.Sprintf("%s:%d", st.cluster.GetHost(), st.cluster.GetPort()))
	kv(b, "UUID", st.cluster.GetUuid())
	if st.snap == nil {
		b.WriteString("\n")
		b.WriteString(styleErr.Render("⨯ недоступен: " + st.lastErr))
		return
	}
	cuuid := st.cluster.GetUuid()
	b.WriteString("\n")
	kv(b, "Баз", fmt.Sprintf("%d", len(st.snap.Infobases[cuuid])))
	kv(b, "Сеансов", fmt.Sprintf("%d", len(st.snap.Sessions[cuuid])))
	kv(b, "Соединений", fmt.Sprintf("%d", len(st.snap.Connections[cuuid])))
	kv(b, "Рабочих процессов", fmt.Sprintf("%d", len(st.snap.Processes[cuuid])))
	kv(b, "Блокировок", fmt.Sprintf("%d", len(st.snap.Locks[cuuid])))
	kv(b, "expiration-timeout", fmt.Sprintf("%d с", st.cluster.GetExpirationTimeout()))
	if len(st.snap.ListErrs) > 0 {
		b.WriteString("\n" + styleWarn.Render("⚠ частичные ошибки списков:"))
		for k, e := range st.snap.ListErrs {
			fmt.Fprintf(b, "\n  %s: %v", k, e)
		}
	}
	for _, p := range st.snap.Processes[cuuid] {
		b.WriteString("\n")
		kv(b, "rphost "+p.GetHost(), fmt.Sprintf("порт %d · pid %s · старт %s · соед: %d · память: %s",
			p.GetPort(), p.GetPid(), ago(p.GetStartedAt().AsTime()), p.GetConnections(),
			humanBytes(int64(p.GetMemorySize())*1024)))
	}
}

// renderClusterSessionsTab — все сеансы кластера по всем базам.
// Текстовый фильтр (/) действует и здесь.
func (m *model) renderClusterSessionsTab(b *strings.Builder, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Сеансы кластера"))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	cuuid := st.cluster.GetUuid()
	ibName := map[string]string{}
	for _, ib := range st.snap.Infobases[cuuid] {
		ibName[ib.GetUuid()] = ib.GetName()
	}
	// шапка таблицы (как в zoom)
	b.WriteString(" " + truncPad("Пользователь", 18) + "  " + truncPad("Приложение", 12) +
		"  " + truncPad("Клиент", 18) + "  " + truncPad("База", 18) + "  " + "Активен" + "\n")
	b.WriteString(strings.Repeat("─", 80) + "\n\n")

	n := 0
	for _, s := range st.snap.Sessions[cuuid] {
		if !m.sessionVisible(s) {
			continue
		}
		n++
		user := s.GetUserName()
		if user == "" {
			user = "—"
		}
		base := ibName[s.GetInfobaseId()]
		if base == "" {
			base = "—"
		}
		// цвета как в дереве/zoom: пользователь зелёным, приложение ярко/красным, хост тускло
		userStyle := styleOnline
		if isJobSession(s.GetAppId()) {
			userStyle = styleJobs
		}
		appStyle := styleVal
		hostStyle := styleSession
		if s.GetAppId() == "Designer" {
			appStyle = styleErr
		}
		if s.GetHibernate() {
			appStyle = styleHibernat
			hostStyle = styleHibernat
			userStyle = styleHibernat
		}
		app := appIDLabel(s.GetAppId())
		host := s.GetHost()
		line := " " + userStyle.Render(user) + spaces(18-runeCount(user)) +
			"  " + appStyle.Render(app) + spaces(12-runeCount(app)) +
			"  " + hostStyle.Render(host) + spaces(18-runeCount(host)) +
			"  " + truncPad(base, 18) +
			"  " + ago(s.GetLastActiveAt().AsTime())
		if s.GetHibernate() {
			line += "  (спит)"
		}
		b.WriteString(line + "\n")
	}
	if n == 0 {
		b.WriteString(styleHibernat.Render(" нет видимых сеансов (фильтр/настройки)\n"))
	}
	b.WriteString("\n" + styleHint.Render(fmt.Sprintf(" видимых: %d из %d", n, len(st.snap.Sessions[cuuid]))))
}

// renderProcessesTab — вкладка рабочих процессов кластера.
func renderProcessesTab(b *strings.Builder, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Рабочие процессы (rphost)"))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	for _, p := range st.snap.Processes[st.cluster.GetUuid()] {
		kv(b, p.GetHost()+":"+fmt.Sprint(p.GetPort()), "")
		kv(b, "  pid", p.GetPid())
		kv(b, "  старт", ago(p.GetStartedAt().AsTime()))
		kv(b, "  состояние", runState(p.GetRunning(), p.GetUse()))
		kv(b, "  соединений", fmt.Sprintf("%d", p.GetConnections()))
		kv(b, "  память", humanBytes(int64(p.GetMemorySize())*1024))
		kv(b, "  производительность", fmt.Sprintf("%d / %d", p.GetAvailablePerfomance(), p.GetCapacity()))
		kv(b, "  ср. время вызова", fmt.Sprintf("%.2f мс", p.GetAvgCallTime()))
		kv(b, "  ср. время СУБД", fmt.Sprintf("%.2f мс", p.GetAvgDbCallTime()))
		b.WriteString("\n")
	}
}

// renderLocksTab — вкладка блокировок кластера.
func renderLocksTab(b *strings.Builder, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Блокировки"))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	locks := st.snap.Locks[st.cluster.GetUuid()]
	if len(locks) == 0 {
		b.WriteString(styleHibernat.Render(" нет блокировок"))
		return
	}
	for _, l := range locks {
		line := l.GetDescription()
		if line == "" {
			line = "объект " + shortUUID(l.GetObjectId())
		}
		fmt.Fprintf(b, " %s %s\n", styleKey.Render(l.GetLockedAt().AsTime().Format("15:04:05")), line)
	}
}

// renderServersTab — вкладка рабочих серверов.
func renderServersTab(b *strings.Builder, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Рабочие серверы"))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	for _, s := range st.snap.Servers[st.cluster.GetUuid()] {
		role := "второстепенный"
		if s.GetMainServer() {
			role = "центральный"
		}
		kv(b, s.GetName(), role)
		kv(b, "  агент", fmt.Sprintf("%s:%d", s.GetAgentHost(), s.GetAgentPort()))
		kv(b, "  лимит баз", fmt.Sprintf("%d", s.GetInfobasesLimit()))
		b.WriteString("\n")
	}
}

// renderManagersTab — вкладка менеджеров кластера.
func renderManagersTab(b *strings.Builder, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Менеджеры кластера"))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	for _, mg := range st.snap.Managers[st.cluster.GetUuid()] {
		role := ""
		if mg.GetMainManager() != 0 {
			role = " · главный"
		}
		kv(b, mg.GetDescr(), fmt.Sprintf("%s:%d%s", mg.GetHost(), mg.GetPort(), role))
		kv(b, "  pid", mg.GetPid())
		b.WriteString("\n")
	}
}

// renderInfobaseLocksTab — блокировки базы: соотносим блокировки кластера
// с базой через её сеансы (и по упоминанию имени базы в описании).
func renderInfobaseLocksTab(b *strings.Builder, st *clusterState, r *row) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Блокировки базы "+r.ib.GetName()))
	if st == nil || st.snap == nil || st.cluster == nil {
		b.WriteString(styleHibernat.Render(" нет данных"))
		return
	}
	cuuid := st.cluster.GetUuid()
	// сеансы базы: session uuid → относится к базе
	mine := map[string]bool{}
	for _, s := range st.snap.Sessions[cuuid] {
		if s.GetInfobaseId() == r.ib.GetUuid() {
			mine[s.GetUuid()] = true
		}
	}
	n := 0
	for _, l := range st.snap.Locks[cuuid] {
		match := mine[l.GetSessionId()] ||
			strings.Contains(l.GetDescription(), r.ib.GetName())
		if !match {
			continue
		}
		n++
		line := l.GetDescription()
		if line == "" {
			line = "объект " + shortUUID(l.GetObjectId())
		}
		fmt.Fprintf(b, " %s %s\n", styleKey.Render(l.GetLockedAt().AsTime().Format("15:04:05")), line)
	}
	if n == 0 {
		b.WriteString(styleHibernat.Render(" нет блокировок"))
	}
}

// renderInfobaseProps — вкладка свойств базы из GetInfobase (лениво).
func (m *model) renderInfobaseProps(b *strings.Builder, r *row) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Свойства базы "+r.ib.GetName()))
	e := m.ibInfo[r.clusterID+"/"+r.ib.GetUuid()]
	switch {
	case e == nil || e.pending:
		b.WriteString(styleHibernat.Render(" загружается…"))
		return
	case e.errText != "":
		b.WriteString(styleErr.Render("⨯ " + e.errText))
		b.WriteString("\n" + styleHint.Render(" a: ввести логин/пароль этой базы (или ib_user в конфиге)"))
		return
	}
	i := e.info
	kv(b, "СУБД", i.GetDbms())
	kv(b, "Сервер БД", i.GetDbServer())
	kv(b, "База данных", i.GetDbName())
	kv(b, "Локаль", i.GetLocale())
	b.WriteString("\n")
	kv(b, "Регл. задания", jobsAllowLabel(!i.GetScheduledJobsDeny()))
	kv(b, "Начало сеансов", allowLabel(!i.GetSessionsDeny()))
	if i.GetDeniedFrom() != nil {
		kv(b, "  с", i.GetDeniedFrom().AsTime().Format("02.01 15:04"))
		kv(b, "  по", i.GetDeniedTo().AsTime().Format("02.01 15:04"))
	}
	if msg := i.GetDeniedMessage(); msg != "" {
		kv(b, "  сообщение", msg)
	}
	if c := i.GetPermissionCode(); c != "" {
		kv(b, "  код разрешения", c)
	}
}

func (m *model) renderInfobaseDetail(b *strings.Builder, r *row, st *clusterState) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Информационная база"))
	kv(b, "Имя", r.ib.GetName())
	kv(b, "UUID", r.ib.GetUuid())
	if d := strings.TrimSpace(r.ib.GetDescr()); d != "" {
		kv(b, "Описание", d)
	}
	// имя БД и флаги — из ленивой карточки (если уже загружена и хватило прав)
	if e := m.ibInfo[r.clusterID+"/"+r.ib.GetUuid()]; e != nil && e.info != nil {
		i := e.info
		if n := i.GetDbName(); n != "" {
			kv(b, "База данных", n)
		}
		if s := i.GetDbServer(); s != "" {
			kv(b, "Сервер БД", s)
		}
		kv(b, "Начало сеансов", allowLabel(!i.GetSessionsDeny()))
		kv(b, "Регл. задания", jobsAllowLabel(!i.GetScheduledJobsDeny()))
	}
	if st != nil && st.snap != nil {
		users, jobs := m.countUsersJobs(st.snap.Sessions[st.cluster.GetUuid()], r.ib.GetUuid())
		b.WriteString("\n")
		kv(b, "Люди (сеансов)", styleOnline.Render(fmt.Sprintf("%d", users)))
		kv(b, "Фоновые сеансы", styleJobs.Render(fmt.Sprintf("%d", jobs)))
	}
}

// allowLabel — «начало сеансов»: разрешено/запрещено.
func allowLabel(allowed bool) string {
	if allowed {
		return styleOnline.Render("разрешено")
	}
	return styleErr.Render("запрещено")
}

// jobsAllowLabel — «регл. задания»: разрешены/запрещены.
func jobsAllowLabel(allowed bool) string {
	if allowed {
		return styleOnline.Render("разрешены")
	}
	return styleErr.Render("запрещены")
}

func renderSessionDetail(b *strings.Builder, s *serializev1.SessionInfo) {
	fmt.Fprintf(b, "\n%s\n\n", styleTitle.Render(" Сеанс"))
	kv(b, "Приложение", fmt.Sprintf("%s (%s)", appIDLabel(s.GetAppId()), s.GetAppId()))
	kv(b, "Пользователь", s.GetUserName())
	kv(b, "Клиент", s.GetHost())
	kv(b, "Начат", ago(s.GetStartedAt().AsTime()))
	kv(b, "Активность", ago(s.GetLastActiveAt().AsTime()))
	kv(b, "Спящий", yesNo(s.GetHibernate()))
	kv(b, "База", shortUUID(s.GetInfobaseId()))
	b.WriteString("\n")
	kv(b, "Байт всего", humanBytes(s.GetBytesAll()))
	kv(b, "Байт за 5 мин", humanBytes(s.GetBytesLast5Min()))
	kv(b, "Байт СУБД", humanBytes(s.GetDbmsBytesAll()))
	kv(b, "Вызовов всего", fmt.Sprintf("%d", s.GetCallsAll()))
	kv(b, "Вызовов за 5 мин", fmt.Sprintf("%d", s.GetCallsLast5Min()))
	kv(b, "Время CPU", humanDur(int64(s.GetDurationAll())))
	kv(b, "Время СУБД", humanDur(int64(s.GetDurationAllDbms())))
	kv(b, "Заблокирован СУБД", fmt.Sprintf("%d", s.GetBlockedByDbms()))
}

func runState(running, use bool) string {
	switch {
	case running && use:
		return "работает"
	case running:
		return "работает, не используется"
	default:
		return "остановлен"
	}
}

func kv(b *strings.Builder, k, v string) {
	fmt.Fprintf(b, " %s %s\n", styleKey.Render(pad(k, 20)), styleVal.Render(v))
}

// pad выравнивает строку до n ячеек (по рунам, не байтам — кириллица 2 байта/руна).
func pad(s string, n int) string {
	for utf8.RuneCountInString(s) < n {
		s += " "
	}
	return s
}

func yesNo(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

func hintText() string {
	return ` Управление:
   j / ↓, k / ↑    навигация
   enter / l       раскрыть-закрыть узел (на базе enter — полный экран)
   [ / ]           вкладки панели деталей
   Tab             следующий кластер
   R               обновить сейчас
   r / b           регл. задания / блокировка сеансов (на базе)
   x               завершить сеанс (y/n)
   q               выход

 Кластеры опрашиваются каждые N секунд из lazy1c.toml`
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
