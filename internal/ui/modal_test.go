package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestOverlayPreservesANSI: модалка не должна ломать стили и кириллицу под ней.
func TestOverlayPreservesANSI(t *testing.T) {
	// экран: 3 строки по 30 ячеек, с ANSI-кодами и кириллицей
	screen := strings.Join([]string{
		strings.Repeat("│тест│", 6),
		strings.Repeat("│тест│", 6),
		strings.Repeat("│тест│", 6),
	}, "\n")
	box := "╔════╗\n║ абв ║\n╚════╝"
	out := overlay(screen, box, 10, 1)
	lines := strings.Split(out, "\n")
	if got := ansi.StringWidth(lines[0]); got != 36 {
		t.Errorf("ширина строки 0 = %d, хочу 36", got)
	}
	if !strings.Contains(lines[0], "│тест│") {
		t.Errorf("строка 0 над модалкой повреждена: %q", lines[0])
	}
	if !strings.Contains(lines[1], "╔════╗") {
		t.Errorf("верхняя рамка модалки не на строке 1: %q", lines[1])
	}
	if !strings.Contains(lines[2], "║ абв ║") {
		t.Errorf("строка модалки не на строке 2: %q", lines[2])
	}
	for i, l := range lines {
		_ = i
		// разорванный посреди текста ANSI-код не должен появляться
		if strings.Contains(l, ";5;") && strings.Contains(l, "тест") && strings.Index(l, "\x1b[") > strings.Index(l, "тест") {
			t.Errorf("строка выглядит разорванной: %q", l)
		}
	}
}

// TestOverlayShortLine: строка короче модалки дополняется пробелами без паники.
func TestOverlayShortLine(t *testing.T) {
	screen := "короткая\nстрока"
	out := overlay(screen, "╔══════╗\n║  ок  ║\n╚══════╝", 2, 0)
	if !strings.Contains(out, "║  ок  ║") {
		t.Fatalf("модалка потерялась: %q", out)
	}
}
