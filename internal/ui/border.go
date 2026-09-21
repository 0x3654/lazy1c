package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// withTitle вписывает заголовок в верхнюю рамку блока:
// ╭─ Заголовок ─────╮ — как заголовки панелей lazydocker.
// Поддерживает одинарную (╭╮) и двойную (╔╗) рамки; ANSI-aware.
func withTitle(panel, title string) string {
	lines := strings.Split(panel, "\n")
	if len(lines) == 0 || strings.TrimSpace(ansi.Strip(title)) == "" {
		return panel
	}
	top := lines[0]
	var open, close rune = '╭', '╮'
	i := strings.Index(top, string(open))
	if i < 0 {
		open, close = '╔', '╗'
		i = strings.Index(top, string(open))
	}
	j := strings.Index(top, string(close))
	if i < 0 || j < 0 || j < i {
		return panel
	}
	head := top[:i]                     // ANSI-префикс (цвет рамки)
	tail := top[j+utf8.RuneLen(close):] // завершающие коды
	w := ansi.StringWidth(top)          // видимая ширина рамки
	titleW := ansi.StringWidth(title)
	// оформление: угол + «─ » + « » + угол = 5 колонок
	dashes := w - 5 - titleW
	if dashes < 1 {
		// заголовок не влезает — обрезаем
		title = ansi.Truncate(title, max(1, w-6), "…")
		titleW = ansi.StringWidth(title)
		dashes = w - 5 - titleW
		if dashes < 1 {
			return panel
		}
	}
	// head — ANSI-коды цвета рамки: повторяем их после заголовка,
	// чтобы правая часть строки (тире и угол) осталась в цвете рамки
	lines[0] = head + string(open) + "─ " + title + " " + head +
		strings.Repeat("─", dashes) + string(close) + tail
	return strings.Join(lines, "\n")
}
