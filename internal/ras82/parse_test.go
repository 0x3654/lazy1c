package ras82

import (
	"testing"
)

// TestParseSamples гоняет парсер на вшитых образцах ответов ragent 8.2
// и печатает дерево — ручная сверка структуры (запускать с -v).
func TestParseSamples(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"кластеры", respClusters},
		{"сеансы", respSessions},
	} {
		p := &parser{b: tc.data}
		var top []node
		for p.i < len(p.b) {
			n, ok := p.value()
			if !ok {
				break
			}
			top = append(top, n)
		}
		t.Logf("=== %s (%dБ) ===\n%s", tc.name, len(tc.data), dumpNodes(top, p, 0))
		if len(top) == 0 {
			t.Errorf("%s: пустой разбор", tc.name)
		}
	}
}

// TestAssembleSessions — сборка записей сеансов из вшитого образца
// (425Б-ответ с двумя сеансами: «123» и «testDB82»).
func TestAssembleSessions(t *testing.T) {
	vals := ParseBin(respSessions)
	ss := assembleSessions(vals)
	resolveSessionIDs(ss)
	if len(ss) < 2 {
		t.Fatalf("сеансов %d, ожидалось ≥2\nзначения: %+v", len(ss), vals)
	}
	for _, s := range ss {
		t.Logf("сеанс: id=%s база=%q хост=%s прил=%s язык=%s вер=%s %v…%v",
			s.ID, s.InfobaseName, s.Host, s.App, s.Locale, s.Ver, s.Started, s.LastActive)
	}
	seen := map[string]bool{}
	for _, s := range ss {
		seen[s.InfobaseName] = true
		if s.Host == "" || s.App == "" {
			t.Errorf("пустые поля в записи: %+v", s)
		}
		// ID в старом VM-захвате может отсутствовать (нет d5-uuid у базы) — ок
	}
	if !seen["123"] && !seen["testDB82"] {
		t.Errorf("имена баз из образца не найдены: %+v", ss)
	}
}
