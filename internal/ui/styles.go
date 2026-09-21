package ui

import (
	"charm.land/lipgloss/v2"

	"lazy1c/internal/config"
)

// Палитра — базовые ANSI-цвета (0–15), как в lazydocker (green/blue/default).
// Терминал с темой (Ghostty, iTerm и т.п.) перекрашивает базовые слоты под
// себя, поэтому весь интерфейс следует палитре системы. Коды 16–255 тоже
// можно задать в [theme] конфига — но они фиксированы и палитре не следуют.
var (
	// переопределяются applyTheme из конфига
	styleBorderActive   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("2")).Bold(true) // green+bold, как lazydocker
	styleBorderInactive = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())                                                  // default — цвет текста терминала

	styleCluster  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")) // циан
	styleInfobase = lipgloss.NewStyle()                                            // default
	styleSession  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // bright black = серый темы
	styleHibernat = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // тусклый
	// курсор — как selectedLineBgColor в lazydocker: базовый слот «blue»,
	// который терминал отрисовывает по своей палитре (у пользователя — оранжевый)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("4"))

	styleOnline  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // зелёный темы
	styleOffline = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // красный темы
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // жёлтый темы
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleMark    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")) // отметка ✓ (пробел) — зелёная галка

	styleKey         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleVal         = lipgloss.NewStyle()                                            // default
	styleTitle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("7")) // заголовки секций — нейтрально
	styleTabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")) // активная вкладка — цвет активной рамки
	styleHint        = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))            // optionsTextColor: blue
	stylePing        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleFlags       = lipgloss.NewStyle().Foreground(lipgloss.Color("7")) // статусы РЗ/Вход — ярче пинга, тише основного
	styleJobs        = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // рег. задания — жёлтым
	styleModal       = lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("1")).Padding(1, 2).Width(46)
	styleModalDanger = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// applyTheme применяет цвета из конфига (пустые значения — базовые слоты
// терминала, как в lazydocker).
func applyTheme(t config.Theme) {
	if t.BorderActive != "" {
		styleBorderActive = styleBorderActive.BorderForeground(lipgloss.Color(t.BorderActive))
	}
	if t.BorderInactive != "" {
		styleBorderInactive = styleBorderInactive.BorderForeground(lipgloss.Color(t.BorderInactive))
	}
	if t.SelectedBg != "" {
		styleSelected = styleSelected.Background(lipgloss.Color(t.SelectedBg))
	}
	if t.SelectedFg != "" {
		styleSelected = styleSelected.Foreground(lipgloss.Color(t.SelectedFg))
	}
	if t.Hints != "" {
		styleHint = styleHint.Foreground(lipgloss.Color(t.Hints))
	}
}
