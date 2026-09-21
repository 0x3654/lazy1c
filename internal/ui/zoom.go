package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// zoomState — полноэкранный просмотр одной информационной базы.
type zoomState struct {
	clusterID string
	ib        *serializev1.InfobaseSummaryInfo
	cursor    int
	scroll    int
}

// zoomSessions — видимые сеансы открытой базы (та же видимость, что в дереве).
func (m *model) zoomSessions() []*serializev1.SessionInfo {
	z := m.zoom
	if z == nil {
		return nil
	}
	st := m.stateByName(z.clusterID)
	if st == nil || st.snap == nil || st.cluster == nil {
		return nil
	}
	var out []*serializev1.SessionInfo
	for _, s := range st.snap.Sessions[st.cluster.GetUuid()] {
		if s.GetInfobaseId() == z.ib.GetUuid() && m.sessionShown(s) {
			out = append(out, s)
		}
	}
	return out
}

// renderZoom — весь экран: шапка базы, таблица сеансов, детали выбранного.
func (m *model) renderZoom() string {
	z := m.zoom
	st := m.stateByName(z.clusterID)
	users, jobs := 0, 0
	if st != nil && st.cluster != nil {
		cuuid := st.cluster.GetUuid()
		var snapSessions []*serializev1.SessionInfo
		if st.snap != nil {
			snapSessions = st.snap.Sessions[cuuid]
		}
		users, jobs = m.countUsersJobs(snapSessions, z.ib.GetUuid())
	}
	// имя базы и кластер — в заголовке рамки; внутри только счётчики
	badge := ""
	if users > 0 || jobs > 0 {
		badge = styleOnline.Render(fmt.Sprintf("👤%d", users)) + " " + styleJobs.Render(fmt.Sprintf("⚙%d", jobs)) + "\n"
	}
	head := badge + "\n"

	sessions := m.zoomSessions()
	// сеанс под курсором могли убить (в т.ч. нами же) — курсор не должен
	// выходить за границы списка между опросами
	if z.cursor >= len(sessions) {
		z.cursor = len(sessions) - 1
	}
	if z.cursor < 0 {
		z.cursor = 0
	}
	var b strings.Builder
	b.WriteString(head)
	if len(sessions) == 0 {
		b.WriteString(styleHibernat.Render(" нет активных сеансов\n"))
	} else {
		b.WriteString(rowLine("Пользователь", "Приложение", "Клиент", "Начат", "Активен") + "\n")
		b.WriteString(strings.Repeat("─", min(100, m.width-2)) + "\n")
		listH := m.height/2 - 5
		top := clamp(z.scroll, 0, max(0, len(sessions)-listH))
		bottom := min(top+listH, len(sessions))
		for i := top; i < bottom; i++ {
			s := sessions[i]
			// сеанс в процессе завершения — серым + красная пометка
			if m.isPendingKill(s.GetUuid()) {
				user := s.GetUserName()
				if user == "" {
					user = "—"
				}
				plain := " " + user + spaces(18-runeCount(user)) +
					"  " + "..." + spaces(9) +
					"  " + s.GetHost() + spaces(18-runeCount(s.GetHost())) +
					"  " + s.GetStartedAt().AsTime().Format("15:04:05") + "  ..."
				line := plain + "  " + styleErr.Render(m.spinFrame()+" завершается")
				if i == z.cursor {
					line = styleSelected.Render(strings.TrimRight(plain+"  "+m.spinFrame()+" завершается", " "))
				}
				b.WriteString(line + "\n")
				continue
			}
			user := s.GetUserName()
			if user == "" {
				user = "—"
			}
			// цвета как в дереве: пользователь зелёным/жёлтым, приложение ярко, хост тускло
			userStyle := styleOnline
			if isJobSession(s.GetAppId()) {
				userStyle = styleJobs
			}
			appStyle := styleVal
			hostStyle := styleSession
			if s.GetAppId() == "Designer" {
				appStyle = styleErr // конфигуратор — красным
			}
			if s.GetHibernate() {
				appStyle = styleHibernat
				hostStyle = styleHibernat
			}

			start := s.GetStartedAt().AsTime().Format("15:04:05")
			active := ago(s.GetLastActiveAt().AsTime())
			plain := rowLine(user, appIDLabel(s.GetAppId()), s.GetHost(), start, active)
			markCell := " "
			if _, ok := m.marked[s.GetUuid()]; ok {
				markCell = styleMark.Render("✓")
				plain = "✓" + plain[1:] // ведущий пробел rowLine → отметка
			}
			badge := ""
			if s.GetHibernate() {
				badge = "  (спит)"
			}

			// собрать цветную строку: стиль + паддинг снаружи (lipgloss стрипает пробелы)
			app := appIDLabel(s.GetAppId())
			host := s.GetHost()
			styled := markCell + userStyle.Render(user) + spaces(18-runeCount(user)) +
				"  " + appStyle.Render(app) + spaces(12-runeCount(app)) +
				"  " + hostStyle.Render(host) + spaces(18-runeCount(host)) +
				"  " + start + spaces(10-runeCount(start)) +
				"  " + active
			if s.GetHibernate() {
				styled += "  " + styleHibernat.Render("(спит)")
			}

			line := styled
			if i == z.cursor {
				line = styleSelected.Render(strings.TrimRight(plain+badge, " "))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
		renderSessionDetail(&b, sessions[z.cursor])
	}
	// подсказки кнопок — только в статус-баре внизу, тут не дублируем.
	// lipgloss Height задаёт минимум, а не обрезку — клипаем контент сами,
	// чтобы окно (со статус-баром из 2 строк) всегда влезало в экран
	title := " ← База " + z.ib.GetName() + " · " + z.clusterID + " "
	body := b.String()
	if n := m.height - 6; n > 0 && len(strings.Split(body, "\n")) > n {
		body = strings.Join(strings.Split(body, "\n")[:n], "\n")
	}
	return withTitle(styleBorderActive.Width(m.width).Height(m.height-4).Render(body), title)
}

// rowLine собирает строку таблицы: колонки выровнены по рунам,
// слишком длинное значение обрезается с «…».
func rowLine(cols ...string) string {
	widths := []int{18, 12, 18, 10, 12}
	var parts []string
	for i, c := range cols {
		parts = append(parts, truncPad(c, widths[i]))
	}
	return " " + strings.Join(parts, "  ")
}

// truncPad — выравнивание до n ячеек: короткие — дополняет, длинные — обрезает с «…».
func truncPad(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = append(r[:n-1], '…')
	}
	for len(r) < n {
		r = append(r, ' ')
	}
	return string(r)
}

func spaces(n int) string {
	if n < 1 {
		return ""
	}
	return strings.Repeat(" ", n)
}

func runeCount(s string) int { return utf8.RuneCountInString(s) }
