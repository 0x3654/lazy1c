package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"lazy1c/internal/discover"
	"lazy1c/internal/ras"
)

// clusterForm — форма добавления кластера «a»: хост (голое имя или сразу
// «адрес:порт» — без порта подставится стандартный 1540), или поиск в сети
// по CIDR (опционально, отдельная секция). Вставка из буфера понимает
// «адрес:порт» целиком (см. applyClusterPaste).
type clusterForm struct {
	host, ranges string
	field        int // 0 — хост, 1 — CIDR для поиска
}

func newClusterForm() *clusterForm {
	return &clusterForm{}
}

// updateClusterForm — клавиши формы добавления кластера.
func (m *model) updateClusterForm(key string) (tea.Model, tea.Cmd) {
	f := m.cform
	switch key {
	case "esc", "q":
		m.cform = nil
	case "tab", "down", "j":
		f.field = (f.field + 1) % 2
	case "up", "k":
		f.field = (f.field + 1) % 2
	case "backspace":
		switch f.field {
		case 0:
			f.host = chop(f.host)
		case 1:
			f.ranges = chop(f.ranges)
		}
	case " ", "space":
		if f.field == 1 {
			f.ranges += " "
		}
	case "f":
		// подставить подсети своих интерфейсов — типовой случай
		if cidrs := discover.LocalCIDRs(); len(cidrs) > 0 {
			f.ranges = strings.Join(cidrs, ",")
			f.field = 1
			m.pushLog("подсети интерфейсов: " + f.ranges)
		} else {
			m.pushLog("свои подсети не определены — введите CIDR руками")
		}
	case "enter":
		if f.field == 0 { // enter на хосте → добавить (типовой путь)
			return m.submitClusterForm()
		}
		if strings.TrimSpace(f.ranges) != "" {
			cmd := m.startScan(f.ranges)
			m.cform = nil
			return m, cmd
		}
		return m.submitClusterForm()
	default:
		if r := []rune(key); len(r) == 1 {
			switch f.field {
			case 0:
				f.host += key
			case 1:
				f.ranges += key
			}
		}
	}
	return m, nil
}

func chop(s string) string {
	if s != "" {
		return s[:len(s)-1]
	}
	return s
}

// applyClusterPaste — вставка из буфера (bracketed paste) в форму добавления
// кластера. «адрес:порт» ложится в хост целиком (порт без хоста не бывает —
// поле порта убрано), CIDR-список — в поле поиска, одиночный токен — хост.
// Всегда замена, не дописывание.
func (m *model) applyClusterPaste(content string) (tea.Model, tea.Cmd) {
	f := m.cform
	if f == nil {
		return m, nil
	}
	s := strings.TrimSpace(content)
	if s == "" {
		return m, nil
	}
	if strings.Contains(s, "/") { // похоже на CIDR-список — в поле поиска
		f.ranges = s
		f.field = 1
		m.pushLog("вставлены подсети: " + s)
		return m, nil
	}
	if _, _, ok := splitHostPort(s); ok {
		f.host = s // адрес:порт одним куском — enter сразу добавит
		f.field = 0
		m.pushLog("вставлено: " + s)
		return m, nil
	}
	if !strings.ContainsAny(s, " \t,") { // одиночный токен — хост (имя или адрес)
		f.host = s
		f.field = 0
		m.pushLog("вставлен хост: " + s)
	}
	return m, nil
}

// splitHostPort — «host:1545» или «[::1]:1545» с валидным портом.
func splitHostPort(s string) (host, port string, ok bool) {
	if strings.HasPrefix(s, "[") { // IPv6 в квадратных скобках
		if i := strings.Index(s, "]"); i > 0 && len(s) > i+1 && s[i+1] == ':' {
			if h, p := s[1:i], s[i+2:]; h != "" && isPort(p) {
				return h, p, true
			}
		}
		return "", "", false
	}
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	host, port = s[:i], s[i+1:]
	if isHost(host) && isPort(port) {
		return host, port, true
	}
	return "", "", false
}

// isPort — цифры и осмысленный диапазон.
func isPort(s string) bool {
	if !isAllDigits(s) || len(s) > 5 {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// isAllDigits — состоит ли только из цифр.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isHost — DNS-имя или IPv4: буквы/цифры/точки/дефисы/подчёркивания.
func isHost(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t:/,[]") {
		return false
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// submitClusterForm — добавить кластер: в состояние и в settings.toml.
// Голое имя сервера дополняется стандартным портом ragent 1540; явный
// «адрес:порт» не трогаем.
func (m *model) submitClusterForm() (tea.Model, tea.Cmd) {
	f := m.cform
	m.cform = nil
	host := strings.TrimSpace(f.host)
	if host == "" {
		m.pushLog("кластер не добавлен: пустой хост")
		return m, nil
	}
	if port := portOf(host); port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			m.pushLog("кластер не добавлен: порт не число — " + port)
			return m, nil
		}
	} else {
		host += ":1540"
	}
	addr := host
	for i := range m.state {
		if m.state[i].address == addr {
			m.pushLog("кластер " + addr + " уже есть")
			return m, nil
		}
	}
	name := addr
	timeout := time.Duration(m.cfg.CommandTimeout) * time.Second
	m.state = append(m.state, clusterState{
		id: name, address: addr,
		conn:    ras.NewConn(addr, timeout),
		enabled: true, expanded: false, firstSnap: true, ibOpen: map[string]bool{},
	})
	m.settings.unremoveCluster(addr)
	m.settings.Clusters = append(m.settings.Clusters, ExtraCluster{Name: name, Address: addr})
	if err := m.settings.save(); err != nil {
		m.pushLog("кластер не сохранён: " + err.Error())
	} else {
		m.pushLog("кластер " + name + " (" + addr + ") добавлен")
	}
	m.rebuild()
	return m, m.pollAll()
}

// renderClusterForm — оверлей формы добавления кластера.
func (m *model) renderClusterForm() string {
	f := m.cform
	row := func(label, val string, active bool) string {
		marker := " "
		if active {
			marker = "▸"
		}
		line := fmt.Sprintf("%s %-8s %s▏", marker, label, val)
		if active {
			return styleTabActive.Render(line)
		}
		return line
	}
	netVal := f.ranges
	if netVal == "" {
		netVal = styleHibernat.Render("необязательно — для поиска")
	}
	s := styleTitle.Render(" Добавить кластер") + "\n\n" +
		row("Хост", f.host, f.field == 0) + "\n\n" +
		styleHibernat.Render(" Поиск в сети (необязательно):") + "\n" +
		row("CIDR", netVal, f.field == 1) + "\n\n" +
		styleHint.Render(" имя или адрес:порт — добавить (без порта — 1540)\n вставка из буфера понимает адрес:порт целиком\n f: подсети · enter: ок · esc: отмена")
	return withTitle(styleModal.Width(56).Render(s), " a ")
}
