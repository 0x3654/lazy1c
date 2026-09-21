package ui

import (
	"charm.land/bubbletea/v2"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/ras"
)

// TestWidthInvariant — инвариант вёрстки: каждая строка экрана занимает
// ровно ширину терминала (ни пикселей съехавших рамок, ни переносов).
// Проверяется основной режим, zoom и меню.
func TestWidthInvariant(t *testing.T) {
	snap := &ras.Snapshot{Clusters: []*serializev1.ClusterInfo{{Uuid: "c1", Name: "K"}},
		Infobases: map[string][]*serializev1.InfobaseSummaryInfo{"c1": {{Uuid: "ib1", Name: "База"}}},
		Sessions: map[string][]*serializev1.SessionInfo{"c1": {
			{Uuid: "s1", InfobaseId: "ib1", AppId: "1CV8C", UserName: "Вася", Host: "h"},
			{Uuid: "s2", InfobaseId: "ib1", AppId: "Designer", UserName: "ДлинноеИмяПользователя", Host: "host", Hibernate: true},
		}}}
	newModel := func() *model {
		m := &model{cfg: &config.Config{}, ibInfo: map[string]*ibEntry{}, settings: defaultSettings()}
		m.state = []clusterState{{id: "кластер", address: "a", cluster: snap.Clusters[0], snap: snap,
			enabled: true, updated: time.Now(), expanded: true, ibOpen: map[string]bool{"ib1": true}}}
		m.width, m.height = 100, 24
		m.rebuild()
		return m
	}

	for _, mode := range []string{"дерево", "zoom", "меню"} {
		m := newModel()
		switch mode {
		case "zoom":
			m.cursor = 1
			m.zoom = &zoomState{clusterID: "кластер", ib: snap.Infobases["c1"][0]}
		case "меню":
			m.menu = &menuState{}
		}
		for i, l := range strings.Split(m.View().Content, "\n") {
			if w := ansi.StringWidth(l); w != 100 {
				t.Errorf("%s: строка %d шириной %d, хочу 100: |%s|", mode, i, w, ansi.Strip(l))
			}
		}
	}
}

// TestOfflineClusterKeepsIdentity: потухший кластер показывает сервер:порт · версию
// с прошлого подключения, а не безликое «Локальный кластер».
func TestOfflineClusterKeepsIdentity(t *testing.T) {
	m := zoomTestModel()
	// 8.5-подобное состояние: было подключение (кластер+версия), сейчас офлайн
	m.state[0].snap = nil
	m.state[0].cluster.Host, m.state[0].cluster.Port = "server1c", 2541
	m.state[0].version = "8.5.1.1522"
	m.width = 160 // личность без обрезки: узкое дерево режет хвост (так задумано)
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"server1c:2541", "8.5.1.1522", "недоступен"} {
		if !strings.Contains(v, want) {
			t.Errorf("у офлайн-кластера нет %q: %s", want, firstLines(v))
		}
	}
	if strings.Contains(v, "Локальный кластер") {
		t.Error("офлайн-кластер показывает безликое имя RAS")
	}
	// ни разу не отвечал — имя из конфига
	m.state[0].cluster = nil
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.View().Content), "t — недоступен") {
		t.Error("без истории подключений должно быть имя из конфига")
	}
}

// TestPausedClusterKeepsData: при паузе (o) кластер показывает последнее известное:
// сервер:порт · версию, счётчики и дочерние базы.
func TestPausedClusterKeepsData(t *testing.T) {
	m := zoomTestModel()
	m.state[0].ibOpen["ib1"] = true
	m.cursor = 0
	m.rebuild()
	m2, _ := m.Update(tea.KeyPressMsg{Code: 0x73}) // s — пауза
	m = m2.(*model)
	if m.state[0].enabled {
		t.Fatal("o не поставила паузу")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"⏸", ":0", "👤2", "B"} {
		if !strings.Contains(v, want) {
			t.Errorf("при паузе пропало %q: %s", want, firstLines(v))
		}
	}
}
