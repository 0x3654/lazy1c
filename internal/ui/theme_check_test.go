package ui

import (
	"strings"
	"testing"
	"time"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

func TestThemeApplied(t *testing.T) {
	snap := &ras.Snapshot{Clusters: []*serializev1.ClusterInfo{{Uuid: "c1", Name: "K"}}}
	m := &model{cfg: &config.Config{}}
	m.state = []clusterState{{id: "t", address: "a", cluster: snap.Clusters[0], snap: snap,
		enabled: true, updated: time.Now(), expanded: true, ibOpen: map[string]bool{}}}
	m.width, m.height = 100, 20
	m.rebuild()
	m.cursor = 0 // выбранная строка — кластер
	v := m.View()
	// зелёная активная рамка: базовый ANSI green → SGR 32
	if !strings.Contains(v.Content, "32m") {
		t.Error("нет зелёной активной рамки (базовый слот green)")
	}
	// фон выбранной строки: базовый ANSI «blue» → SGR 44 (у пользователя слот = оранжевый)
	if !strings.Contains(v.Content, "44m") {
		t.Error("нет плашки курсора (базовый ANSI-цвет)")
	}
	// синие подсказки: базовый ANSI blue → SGR 34
	if !strings.Contains(v.Content, "34m") {
		t.Error("нет синих подсказок (базовый слот blue)")
	}
}
