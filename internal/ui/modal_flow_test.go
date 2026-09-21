package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

func TestTerminateModalFlow(t *testing.T) {
	cuuid := "c1"
	ibid := "ib1"
	snap := &ras.Snapshot{Clusters: []*serializev1.ClusterInfo{{Uuid: cuuid, Name: "K"}},
		Infobases: map[string][]*serializev1.InfobaseSummaryInfo{cuuid: {{Uuid: ibid, Name: "B"}}},
		Sessions:  map[string][]*serializev1.SessionInfo{cuuid: {{Uuid: "s1", InfobaseId: ibid, AppId: "Designer", UserName: "U", Host: "h"}}},
	}
	m := &model{cfg: &config.Config{}}
	m.state = []clusterState{{id: "t", address: "a", cluster: snap.Clusters[0], snap: snap,
		updated: time.Now(), expanded: true, ibOpen: map[string]bool{}}}
	m.width, m.height = 100, 24
	m.rebuild()
	// раскрыть базу: курсор на базу, toggle
	m.cursor = 1
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x6C}) // "l"
	m = m2.(*model)
	t.Logf("rows=%d cursor=%d kinds=%v", len(m.rows), m.cursor, kinds(m.rows))
	// вниз на сеанс
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // "j"
	m = m2.(*model)
	t.Logf("cursor=%d kind session? %v", m.cursor, m.rows[m.cursor].kind == rowSession)
	// x — открыть подтверждение
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x64}) // "d"
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("подтверждение не открылось")
	}
	v := m.View()
	if !strings.Contains(v.Content, "Завершить сеанс") {
		t.Fatalf("модалки нет на экране:\n%s", v.Content)
	}
	// n — закрыть
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6E}) // "n"
	m = m2.(*model)
	if m.confirm != nil {
		t.Fatal("подтверждение не закрылось по n")
	}
	if strings.Contains(m.View().Content, "Завершить сеанс") {
		t.Fatal("модалка осталась на экране")
	}
}

func kinds(rows []row) []int {
	out := []int{}
	for _, r := range rows {
		out = append(out, int(r.kind))
	}
	return out
}
