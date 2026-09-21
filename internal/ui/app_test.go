package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"testing"
	"time"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

// TestRenderSmoke проверяет, что экран собирается из синтетического снапшота:
// видны кластер, база, сеанс, подсказки; без паники на пустых состояниях.
func TestRenderSmoke(t *testing.T) {
	cuuid := "741f26bd-9ed7-452f-9356-d427b31a5086"
	snap := &ras.Snapshot{
		At:       time.Now(),
		Clusters: []*serializev1.ClusterInfo{{Uuid: cuuid, Name: "Локальный кластер", Host: "server1c", Port: 1541}},
		Infobases: map[string][]*serializev1.InfobaseSummaryInfo{
			cuuid: {{Uuid: "171de230-d590-4260-bead-e2cf6efbd466", Name: "ERP_FF"}},
		},
		Sessions: map[string][]*serializev1.SessionInfo{
			cuuid: {{Uuid: "6e8de796-0fec-4fe8-b0ab-7a8f88e1f09f",
				InfobaseId: "171de230-d590-4260-bead-e2cf6efbd466",
				AppId:      "Designer", UserName: "DefUser", Host: "client.local", Hibernate: true}},
		},
		Connections: map[string][]*serializev1.ConnectionInfo{cuuid: {}},
		Processes:   map[string][]*serializev1.ProcessInfo{cuuid: {}},
		Managers:    map[string][]*serializev1.ManagerInfo{cuuid: {}},
		Servers:     map[string][]*serializev1.ServerInfo{cuuid: {}},
		Locks:       map[string][]*serializev1.LockInfo{cuuid: {}},
		ListErrs:    map[string]error{},
	}

	m := &model{cfg: &config.Config{RefreshInterval: 5}, settings: Settings{HideRAS: true, HideIdle: false, ShowFlags: true}, pendingKills: map[string]string{}}
	m.state = []clusterState{{
		id: "test", address: "localhost:1545", cluster: snap.Clusters[0],
		snap: snap, updated: time.Now(), expanded: true,
		ibOpen: map[string]bool{"171de230-d590-4260-bead-e2cf6efbd466": true},
	}}
	m.width, m.height = 120, 30
	m.rebuild()
	m.cursor = 2 // сеанс
	v := m.View()

	for _, want := range []string{"ERP_FF", "👤1", "Designer", "DefUser", "Пользователь", "выход"} {
		if !strings.Contains(ansi.Strip(v.Content), want) {
			t.Errorf("на экране нет %q", want)
		}
	}
	if strings.Contains(v.Content, "⚙") {
		t.Errorf("нулевые рег. задания не должны показываться")
	}

	// пустое состояние (второй кластер offline) не должно паниковать
	m.state = append(m.state, clusterState{id: "down", address: "x:1", lastErr: "connection refused"})
	m.rebuild()
	_ = m.View()
}

// TestCountUsersJobs: 👤 считает сеансы людей (один пользователь в двух
// клиентах — два сеанса), регламентные и скрытые — отдельно.
func TestCountUsersJobs(t *testing.T) {
	sessions := []*serializev1.SessionInfo{
		{InfobaseId: "ib1", AppId: "1CV8C", UserName: ""}, // аноним — тоже человек
		{InfobaseId: "ib1", AppId: "1CV8C", UserName: "Вася"},
		{InfobaseId: "ib1", AppId: "Designer", UserName: "Вася"},  // тот же Вася, конфигуратор — 2-й сеанс
		{InfobaseId: "ib1", AppId: "BackgroundJob", UserName: ""}, // регл. — не человек
		{InfobaseId: "ib2", AppId: "1CV8C", UserName: "Петя"},     // другая база
	}
	m := &model{settings: Settings{HideRAS: true, HideIdle: false, ShowFlags: true}}
	users, jobs := m.countUsersJobs(sessions, "ib1")
	if users != 3 || jobs != 1 {
		t.Fatalf("users=%d jobs=%d, хочу 3 и 1", users, jobs)
	}
}
