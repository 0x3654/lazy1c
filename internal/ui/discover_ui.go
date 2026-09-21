package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"lazy1c/internal/discover"
	"lazy1c/internal/ras"
)

// scanState — поиск кластеров в сети: скан по CIDR, выбор находок чекбоксами.
type scanState struct {
	ranges  string            // исходные диапазоны (для шапки)
	running bool              // скан идёт
	results []discover.Result // найденные RAS-серверы
	checked []bool            // выбор пользователем
	cursor  int               // курсор в списке
	err     string            // ошибка скана
}

// scanDoneMsg — итог скана (Cmd возвращает одним сообщением).
type scanDoneMsg struct {
	results []discover.Result
	err     error
}

// scanFn — функция скана (переопределяется в тестах).
var scanFn = func(ctx context.Context, cidrs []string, ports []int, timeout time.Duration, progress func(discover.Result)) ([]discover.Result, error) {
	return discover.Scan(ctx, cidrs, ports, timeout, progress)
}

// startScan — запустить скан из строки диапазонов (через запятую).
// Возвращает tea.Cmd: пока он работает, окно показывает «сканирую…».
func (m *model) startScan(ranges string) tea.Cmd {
	cidrs := strings.FieldsFunc(ranges, func(r rune) bool {
		return r == ',' || r == ' '
	})
	if len(cidrs) == 0 {
		m.pushLog("скан: не заданы диапазоны")
		return nil
	}
	m.scan = &scanState{ranges: ranges, running: true}
	m.pushLog("скан сети: " + ranges + " …")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		results, err := scanFn(ctx, cidrs, nil, 400*time.Millisecond, nil)
		return scanDoneMsg{results: results, err: err}
	}
}

// updateScan — клавиши списка находок.
func (m *model) updateScan(key string) (tea.Model, tea.Cmd) {
	s := m.scan
	if s == nil {
		return m, nil
	}
	if s.running { // во время скана можно только отменить
		if key == "esc" || key == "q" {
			m.scan = nil
			m.pushLog("скан отменён")
		}
		return m, nil
	}
	switch key {
	case "esc", "q":
		m.scan = nil
	case "j", "down":
		if s.cursor < len(s.results)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case " ", "space":
		if s.cursor < len(s.checked) {
			s.checked[s.cursor] = !s.checked[s.cursor]
		}
	case "enter", "a":
		added := 0
		timeout := time.Duration(m.cfg.CommandTimeout) * time.Second
		for i, r := range s.results {
			if i >= len(s.checked) || !s.checked[i] {
				continue
			}
			if m.hasCluster(r.Addr) || m.hasClusterID(r.ClusterID) {
				continue
			}
			name := r.Addr
			if r.Version != "" {
				name = r.Version + " · " + hostOf(r.Addr)
			}
			m.state = append(m.state, clusterState{
				id: name, address: r.Addr,
				conn:    ras.NewConn(r.Addr, timeout),
				enabled: true, expanded: false, firstSnap: true, ibOpen: map[string]bool{},
			})
			m.settings.unremoveCluster(r.Addr)
			m.settings.Clusters = append(m.settings.Clusters, ExtraCluster{Name: name, Address: r.Addr})
			added++
		}
		if added > 0 {
			if err := m.settings.save(); err != nil {
				m.pushLog(fmt.Sprintf("добавлено %d, не сохранено: %v", added, err))
			} else {
				m.pushLog(fmt.Sprintf("из скана добавлено кластеров: %d", added))
			}
			m.rebuild()
		} else {
			m.pushLog("из скана ничего не выбрано/добавлено")
		}
		m.scan = nil
		return m, m.pollAll()
	}
	return m, nil
}

// hasClusterID — есть ли уже кластер с этим uuid (та же база через другой интерфейс).
func (m *model) hasClusterID(id string) bool {
	if id == "" {
		return false
	}
	for i := range m.state {
		if m.state[i].cluster != nil && m.state[i].cluster.GetUuid() == id {
			return true
		}
	}
	return false
}

// hasCluster — есть ли уже такой адрес.
func (m *model) hasCluster(addr string) bool {
	for i := range m.state {
		if m.state[i].address == addr {
			return true
		}
	}
	return false
}

func hostOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

// renderScan — оверлей скана/результатов.
func (m *model) renderScan() string {
	s := m.scan
	var b strings.Builder
	if s.running {
		b.WriteString(styleTitle.Render(" Поиск кластеров 1С") + "\n\n")
		b.WriteString(styleHibernat.Render(" сканирую " + s.ranges + " …\n"))
		for _, r := range s.results {
			fmt.Fprintf(&b, "  %s · %s\n", r.Addr, r.Version)
		}
		b.WriteString("\n" + styleHint.Render(" esc: отмена"))
		return withTitle(styleModal.Width(52).Render(b.String()), " Discover ")
	}
	b.WriteString(styleTitle.Render(fmt.Sprintf(" Найдено RAS-серверов: %d", len(s.results))) + "\n\n")
	if s.err != "" {
		b.WriteString(styleErr.Render("⨯ "+s.err) + "\n\n")
	}
	for i, r := range s.results {
		box := " "
		if i < len(s.checked) && s.checked[i] {
			box = "✓"
		}
		note := ""
		if m.hasCluster(r.Addr) || m.hasClusterID(r.ClusterID) {
			note = stylePing.Render("  (уже добавлен)")
		}
		ver := r.Version
		if ver == "" {
			ver = "—"
		}
		line := fmt.Sprintf("  [%s] %-22s %-14s кластеров: %d%s", box, r.Addr, ver, r.Clusters, note)
		if i == s.cursor {
			line = styleSelected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(s.results) == 0 && s.err == "" {
		b.WriteString(styleHibernat.Render(" ничего не найдено") + "\n")
	}
	b.WriteString("\n" + styleHint.Render(" пробел: [x], enter: добавить выбранное, esc: отмена"))
	return withTitle(styleModal.Width(58).Render(b.String()), " Discover ")
}

// scanHeight — высота окна скана (для позиционирования).
