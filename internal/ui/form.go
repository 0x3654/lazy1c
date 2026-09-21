package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"lazy1c/internal/ras"
)

// formState — оверлей ввода логина/пароля базы (как credential-prompt
// в windows-консоли): показывается в «Свойствах», если карточку не удалось
// получить из-за прав. Галка «сохранить» пишет креды с привязкой
// кластер → база и дальше они подставляются автоматически.
type formState struct {
	clusterID, ibID, ibName, clusterName string
	target                               string // "base" — креды базы, "default" — дефолт кластера
	user, pwd                            string
	field                                int // 0 — логин, 1 — пароль, 2 — сохранить
	save                                 bool
}

// openCredsForm — форма кредов из меню: baseName="" значит единый дефолт.
func (m *model) openCredsForm(clusterName, baseName string) {
	user, pwd := "", ""
	if baseName == "" {
		if d, ok := m.settings.ibDefault(); ok {
			user, pwd = d.User, d.Pwd
		}
		clusterName = "" // дефолт общий
	} else if c, ok := m.settings.ibCred(clusterName, baseName); ok {
		user, pwd = c.User, c.Pwd
	}
	st := m.stateByName(clusterName)
	clusterID := ""
	if st != nil {
		clusterID = st.id
	}
	m.form = &formState{
		clusterID: clusterID, clusterName: clusterName,
		ibName: baseName, target: "base",
		user: user, pwd: pwd, save: true,
	}
	if baseName == "" {
		m.form.target = "default"
	}
}

// updateForm — клавиши открытой формы.
func (m *model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	switch msg.String() {
	case "esc", "q":
		m.formDismissed[f.clusterID+"/"+f.ibName] = true // больше не навязываем
		m.form = nil
	case "tab", "down", "j":
		f.field = (f.field + 1) % 3
	case "shift+tab", "up", "k":
		f.field = (f.field + 2) % 3
	case " ", "space":
		if f.field == 2 {
			f.save = !f.save
		} else if f.field == 0 {
			f.user += " "
		} else {
			f.pwd += " "
		}
	case "backspace":
		if f.field == 0 && f.user != "" {
			f.user = f.user[:len(f.user)-1]
		} else if f.field == 1 && f.pwd != "" {
			f.pwd = f.pwd[:len(f.pwd)-1]
		}
	case "enter":
		if f.field < 2 { // enter в полях — следующее поле
			f.field++
			return m, nil
		}
		return m.submitForm()
	default:
		if s := msg.String(); len([]rune(s)) == 1 { // печатаемый символ
			if f.field == 0 {
				f.user += s
			} else if f.field == 1 {
				f.pwd += s
			}
		}
	}
	return m, nil
}

// submitForm — применить введённые креды: карточка + (опционально) сохранение.
func (m *model) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	m.form = nil
	if f.save {
		if f.target == "default" {
			// пустые поля = удалить дефолт
			m.settings.setIBDefault(f.user, f.pwd)
			m.pushLog("дефолт ИБ обновлён (settings.toml)")
		} else if f.user != "" {
			m.settings.setIBCred(IBCred{Cluster: f.clusterName, Base: f.ibName, User: f.user, Pwd: f.pwd})
			m.pushLog("креды базы " + f.ibName + " сохранены (settings.toml)")
		} else {
			m.settings.removeIBCred(f.clusterName, f.ibName)
			m.pushLog("креды базы " + f.ibName + " удалены")
		}
		if err := m.settings.save(); err != nil {
			m.pushLog("не сохранено: " + err.Error())
		}
	}
	// дефолт действует на все кластеры — перечитать всё; для базы — одну карточку
	if f.target == "default" {
		return m, m.pollAll()
	}
	idx := m.indexOfState(f.clusterID)
	cuuid := ""
	if st := m.stateByName(f.clusterID); st != nil && st.cluster != nil {
		cuuid = st.cluster.GetUuid()
	}
	creds := ras.Creds{User: f.user, Pwd: f.pwd}
	ibID := f.ibID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := m.state[idx].conn.GetInfobaseAuth(ctx, cuuid, ibID, creds)
		return ibInfoMsg{clusterID: f.clusterID, ibID: ibID, info: info, err: err}
	}
}

// renderForm — оверлей формы поверх экрана.
func (m *model) renderForm() string {
	f := m.form
	row := func(label, val string, active bool, mask bool) string {
		shown := val
		if mask {
			shown = strings.Repeat("•", len([]rune(val)))
		}
		marker := " "
		if active {
			marker = "▸"
		}
		line := fmt.Sprintf("%s %-10s %s▏", marker, label, shown)
		if active {
			return styleTabActive.Render(line)
		}
		return line
	}
	box := " "
	if f.save {
		box = "✓"
	}
	save := fmt.Sprintf("  [%s] сохранить", box)
	if f.target == "base" {
		save += " для этой базы"
	}
	if f.field == 2 {
		save = styleTabActive.Render("▸ " + strings.TrimLeft(save, " "))
	}
	title := fmt.Sprintf(" Доступ к базе %s", f.ibName)
	if f.target == "default" {
		title = " Дефолт ИБ на все кластеры (пусто = удалить)"
	}
	s := styleTitle.Render(title) + "\n\n" +
		row("Логин", f.user, f.field == 0, false) + "\n" +
		row("Пароль", f.pwd, f.field == 1, true) + "\n\n" +
		save + "\n\n" +
		styleHint.Render(" enter: далее/ок, esc: отмена")
	return withTitle(styleModal.Width(46).Render(s), " Вход ")
}

// maybeOpenForm — открыть форму, если у базы ошибка прав и креды не сохранены.
// Вызывается при показе вкладки «Свойства». force (клавиша a) открывает
// всегда — в том числе чтобы сменить сохранённые креды.
func (m *model) maybeOpenForm(r *row, force bool) {
	e := m.ibInfo[r.clusterID+"/"+r.ib.GetUuid()]
	if !force {
		if e == nil || e.errText == "" {
			return // карточка есть или ещё не пробовали
		}
		if _, saved := m.settings.ibCred(m.stateByName(r.clusterID).id, r.ib.GetName()); saved {
			return // сохранённые креды уже применяются
		}
		if m.formDismissed[r.clusterID+"/"+r.ib.GetName()] {
			return // пользователь закрыл форму — не навязываем
		}
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	// при смене кредов — предзаполним сохранёнными
	user, pwd := "", ""
	if c, ok := m.settings.ibCred(st.id, r.ib.GetName()); ok {
		user, pwd = c.User, c.Pwd
	}
	m.form = &formState{
		clusterID: r.clusterID, ibID: r.ib.GetUuid(),
		ibName: r.ib.GetName(), clusterName: st.id, target: "base",
		user: user, pwd: pwd, save: true,
	}
}
