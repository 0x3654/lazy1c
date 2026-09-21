package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestPlainRowsNoANSI: «plain»-строки дерева (основа курсорной плашки)
// не должны содержать ANSI-кодов — иначе цвета строк просачиваются
// сквозь однотонную плашку курсора (регрессия с эмодзи-счётчиками 👤).
func TestPlainRowsNoANSI(t *testing.T) {
	m := filterTestModel()
	m.width, m.height = 140, 30
	m.state[0].expanded = true
	m.state[0].ibOpen["ib-erp"] = true
	m.rebuild()
	kinds := map[int]bool{}
	for i := range m.rows {
		r := m.rows[i]
		_, plain := m.rowText(&r)
		kinds[int(r.kind)] = true
		if strings.Contains(plain, "\x1b[") {
			t.Errorf("строка %d (kind=%d) plain содержит ANSI: %q", i, r.kind, plain)
		}
	}
	for _, k := range []int{int(rowCluster), int(rowInfobase), int(rowSession)} {
		if !kinds[k] {
			t.Errorf("в тестовой модели нет строк типа %d — проверка неполная", k)
		}
	}
}

// TestCursorBarFullRow: плашка курсора начинается у левого края панели
// и заливает строку до правого края — на строке с эмодзи (👤 = 2 ячейки).
func TestCursorBarFullRow(t *testing.T) {
	m := filterTestModel()
	m.width, m.height = 140, 30
	m.state[0].expanded = true
	m.rebuild()
	m.cursor = 0 // кластер: эмодзи-счётчики в строке
	m.rebuild()
	treeW := m.width * 38 / 100
	v := m.renderTree(treeW, 20)
	lines := strings.Split(v, "\n")
	row := lines[1] // первая строка контента
	if w := ansi.StringWidth(row); w != treeW {
		t.Fatalf("ширина строки с курсором %d, хочу %d", w, treeW)
	}
	// плашка сразу после рамки и её bold-обёртки, без прозрачного отступа
	prefix := "\x1b[32m│\x1b[m\x1b[1m" // рамка зелёная (фокус 0) + bold стиля рамки
	if !strings.HasPrefix(row, prefix+"\x1b[1;30;44m") {
		t.Fatalf("плашка не у левого края: %q", row)
	}
	// внутри плашки (между открывающим и закрывающим кодом) чужих кодов нет
	open := "\x1b[1;30;44m"
	start := strings.Index(row, open) + len(open)
	end := start + strings.Index(row[start:], "\x1b[m")
	if seg := row[start:end]; strings.Contains(seg, "\x1b[") {
		t.Errorf("в плашку просачивается чужой цвет: %q", seg)
	}
	// фон до правого края: до закрывающего кода — ячейки, а не пустота
	if ansi.StringWidth(ansi.Strip(row[start:end])) != treeW-2 {
		t.Errorf("плашка не на всю ширину: %d ячеек из %d", ansi.StringWidth(ansi.Strip(row[start:end])), treeW-2)
	}
}
