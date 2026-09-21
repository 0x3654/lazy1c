package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/discover"
	"lazy1c/internal/ras"
)

// TestAddClusterForm: a открывает форму, адрес добавляет кластер и сохраняет его.
// Голое имя дополняется портом 1540, явный «адрес:порт» не трогается.
func TestAddClusterForm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()

	m2, _ := m.Update(tea.KeyPressMsg{Code: 'a'})
	m = m2.(*model)
	if m.cform == nil {
		t.Fatal("+ не открыл форму кластера")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"Добавить кластер", "Хост", "CIDR", "необязательно"} {
		if !strings.Contains(v, want) {
			t.Fatalf("в форме нет %q: %s", want, firstLines(v))
		}
	}
	if strings.Contains(v, "Порт") {
		t.Fatal("поля Порт быть не должно")
	}
	for _, ch := range "10.0.0.5" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter на хосте — добавить
	m = m2.(*model)
	if m.cform != nil {
		t.Fatal("форма не закрылась")
	}
	if len(m.state) != 2 || m.state[1].address != "10.0.0.5:1540" || m.state[1].id != "10.0.0.5:1540" {
		t.Fatalf("голому имени не подставился порт 1540: %+v", m.state)
	}
	if len(m.settings.Clusters) != 1 || m.settings.Clusters[0].Address != "10.0.0.5:1540" {
		t.Fatalf("кластер не сохранён: %+v", m.settings.Clusters)
	}
	// дубль по адресу не добавляется
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'a'})
	m = m2.(*model)
	for _, ch := range "10.0.0.5" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if len(m.state) != 2 {
		t.Fatalf("дубль кластера добавился: %d", len(m.state))
	}
	// явный адрес:порт сохраняется как есть
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'a'})
	m = m2.(*model)
	for _, ch := range "10.0.0.6:1545" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D})
	m = m2.(*model)
	if len(m.state) != 3 || m.state[2].address != "10.0.0.6:1545" {
		t.Fatalf("явный порт не сохранён: %+v", m.state)
	}
}

// TestEmptyTreeHint: без кластеров дерево показывает подсказку про a.
func TestEmptyTreeHint(t *testing.T) {
	m := zoomTestModel()
	m.state = nil
	m.rebuild()
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "нет кластеров") || !strings.Contains(v, "a: добавить") {
		t.Fatalf("нет подсказки пустого дерева: %s", firstLines(v))
	}
}

// TestDiscoverFlow: поле Сеть в форме + запускает скан (фейковый), находки
// выбираются чекбоксами, enter добавляет (дубли отсекаются).
func TestDiscoverFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// фейковый скан: два сервера, один уже добавлен (localhost:1545)
	scanFn = func(ctx context.Context, cidrs []string, ports []int, timeout time.Duration, progress func(discover.Result)) ([]discover.Result, error) {
		return []discover.Result{
			{Addr: "10.0.0.5:1545", Version: "8.3.27.2325", Clusters: 1},
			{Addr: "localhost:1545", Version: "8.3.27.2325", Clusters: 1},
		}, nil
	}
	defer func() { scanFn = discover.Scan }()

	m := zoomTestModel() // уже есть localhost-кластер? нет: адрес "a" — значит дублей нет
	m.settings = defaultSettings()
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'a'})
	m = m2.(*model)
	// j — на CIDR (хост → CIDR)
	for i := 0; i < 1; i++ {
		m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A})
		m = m2.(*model)
	}
	for _, ch := range "10.0.0.0/24" {
		m2, _ = m.Update(tea.KeyPressMsg{Code: ch})
		m = m2.(*model)
	}
	cmd := func() tea.Msg { return nil }
	_ = cmd
	m2, c := m.Update(tea.KeyPressMsg{Code: 0x0D}) // enter на Сети — скан
	m = m2.(*model)
	if m.scan == nil || !m.scan.running {
		t.Fatal("скан не запущен")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "сканирую") {
		t.Fatalf("нет индикатора скана: %s", firstLines(v))
	}
	// результат скана
	if c == nil {
		t.Fatal("startScan не вернул Cmd")
	}
	msg := c()
	m2, _ = m.Update(msg)
	m = m2.(*model)
	if m.scan == nil || m.scan.running {
		t.Fatal("скан не завершён")
	}
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "10.0.0.5:1545") || !strings.Contains(v, "Найдено") {
		t.Fatalf("находки не показаны: %s", firstLines(v))
	}
	// второй пункт (localhost:1545) — уже не дубль в этой модели; снимем его
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x6A}) // на второй пункт
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x20}) // снять галку
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyPressMsg{Code: 0x0D}) // добавить выбранный
	m = m2.(*model)
	if m.scan != nil {
		t.Fatal("окно скана не закрылось")
	}
	if len(m.state) != 2 || m.state[1].address != "10.0.0.5:1545" {
		t.Fatalf("кластер из скана не добавлен: %+v", m.state)
	}
	if m.state[1].id != "8.3.27.2325 · 10.0.0.5" {
		t.Fatalf("имя из версии не собрано: %q", m.state[1].id)
	}
	if len(m.settings.Clusters) != 1 {
		t.Fatalf("не сохранён: %+v", m.settings.Clusters)
	}
}

// TestClusterFormPlaceholderAndF: пустое «Сеть» показывает призрачный пример,
// f подставляет подсети интерфейсов.
func TestClusterFormPlaceholderAndF(t *testing.T) {
	m := zoomTestModel()
	m.settings = defaultSettings()
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'a'})
	m = m2.(*model)
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "необязательно") { // плейсхолдер «необязательно»
		t.Fatalf("нет плейсхолдера: %s", firstLines(v))
	}
	if !strings.Contains(v, "f: подсети") {
		t.Fatal("нет подсказки про f")
	}
	if m.cform.ranges != "" {
		t.Fatal("CIDR должно быть пустым")
	}
	m2, _ = m.Update(tea.KeyPressMsg{Code: 'f'})
	m = m2.(*model)
	if m.cform.ranges == "" {
		t.Skip("интерфейсов без IPv4 нет — пропуск (в CI/контейнере)")
	}
	if !strings.Contains(m.cform.ranges, "/") {
		t.Fatalf("f не подставил CIDR: %q", m.cform.ranges)
	}
	if m.cform.field != 1 {
		t.Fatal("f должен переводить курсор на поле CIDR")
	}
}

// TestCollapseExpandAll: Shift+→ — трёхуровневое разворачивание (локально),
// Shift+← на кластере — глобальное сворачивание.
func TestCollapseExpandAll(t *testing.T) {
	m := zoomTestModel()
	m.state[0].expanded = false
	m.cursor = 0

	m.expandAll() // 1: только активные
	if !m.state[0].expanded || !m.state[0].expandOnlyActive {
		t.Fatal("уровень 1 не работает")
	}
	m.expandAll() // 2: все базы
	if m.state[0].expandOnlyActive {
		t.Fatal("уровень 2 не работает")
	}
	m.expandAll() // 3: до сеансов
	if !m.state[0].ibOpen["ib1"] {
		t.Fatal("уровень 3 не работает")
	}
	// Shift+← на кластере — глобально свернуть всё
	m.cursor = 0
	m.collapseAll()
	if m.state[0].expanded || len(m.state[0].ibOpen) != 0 || m.state[0].expandOnlyActive {
		t.Fatal("Shift+← на кластере должен свернуть всё")
	}
}

// TestRemoveCluster: D (Shift+D) убирает кластер из списка (конфигный — в removed, UIшный — из clusters),
// сервер не трогается; повторное добавление адреса возвращает его.
func TestRemoveCluster(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel() // state[0] — как конфигный (address "a")
	m.settings = defaultSettings()
	m.cursor = 0
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'D'})
	m = m2.(*model)
	if m.confirm == nil {
		t.Fatal("D не запросил подтверждение")
	}
	if !strings.Contains(m.confirm.question, "не затрагивается") {
		t.Fatalf("вопрос не объясняет безвредность: %q", m.confirm.question)
	}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("подтверждение не вернуло команду")
	}
	m2, _ = m.Update(cmd()) // clusterRemovedMsg
	m = m2.(*model)
	if len(m.state) != 0 {
		t.Fatal("кластер не убран из состояния")
	}
	if !m.settings.isRemovedCluster("a") {
		t.Fatal("адрес не попал в removed_clusters")
	}
	// повторное добавление адреса — убирает из removed
	m.settings.unremoveCluster("a")
	if m.settings.isRemovedCluster("a") {
		t.Fatal("unremove не сработал")
	}
}

// TestMassRemoveNoPanic: массовое удаление при летящих опросах не паникует —
// поздние snapshotMsg с протухшими индексами игнорируются.
func TestMassRemoveNoPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := zoomTestModel()
	m.settings = defaultSettings()
	// 3 «мусорных» кластера поверх базового
	for _, addr := range []string{"10.0.0.2:1545", "10.0.0.3:1545", "10.0.0.4:1545"} {
		m.state = append(m.state, clusterState{id: addr, address: addr,
			conn: ras.NewConn(addr, 50*time.Millisecond), enabled: true, ibOpen: map[string]bool{}})
	}
	m.rebuild()

	// убираем кластеры 0..2 по очереди, подтверждая и «дожимая» команды
	for len(m.state) > 1 {
		m.cursor = 0
		m2, _ := m.Update(tea.KeyPressMsg{Code: 'D'})
		m = m2.(*model)
		if m.confirm == nil {
			t.Fatal("нет подтверждения удаления")
		}
		m2, cmd := m.Update(tea.KeyPressMsg{Code: 'y'})
		m = m2.(*model)
		if cmd == nil {
			t.Fatal("y не вернул команду удаления")
		}
		m2, _ = m.Update(cmd()) // clusterRemovedMsg
		m = m2.(*model)
	}
	// поздний опрос убранного кластера со старым индексом — не паникует
	var m2 tea.Model
	m2, _ = m.Update(tea.Msg(snapshotMsg{stateIdx: 14, snap: nil, err: nil}))
	m = m2.(*model)
	m2, _ = m.Update(tea.Msg(actionDoneMsg{stateIdx: 14, what: "bulk x", err: nil}))
	m = m2.(*model)
	if len(m.state) != 1 {
		t.Fatalf("осталось кластеров: %d", len(m.state))
	}
	_ = m.View()
}

// TestInfobaseOrderStable: список баз отсортирован по алфавиту и не прыгает
// при пересортировке входного массива снапшота (разные движки отдают по-разному).
func TestInfobaseOrderStable(t *testing.T) {
	m := zoomTestModel()
	snap := m.state[0].snap
	snap.Infobases["c1"] = []*serializev1.InfobaseSummaryInfo{
		{Uuid: "ib-z", Name: "zeta"}, {Uuid: "ib1", Name: "B"}, {Uuid: "ib-a", Name: "alpha"},
	}
	m.state[0].ibOpen = map[string]bool{}
	m.rebuild()
	names := baseNames(m)
	if names[0] != "alpha" || names[1] != "B" || names[2] != "zeta" {
		t.Fatalf("порядок не алфавитный: %v", names)
	}
	// переворачиваем вход — порядок на экране тот же
	snap.Infobases["c1"] = []*serializev1.InfobaseSummaryInfo{
		{Uuid: "ib1", Name: "B"}, {Uuid: "ib-a", Name: "alpha"}, {Uuid: "ib-z", Name: "zeta"},
	}
	m.rebuild()
	again := baseNames(m)
	for i := range names {
		if names[i] != again[i] {
			t.Fatalf("список прыгнул при обновлении: %v → %v", names, again)
		}
	}
}

func baseNames(m *model) (out []string) {
	for _, r := range m.rows {
		if r.kind == rowInfobase {
			out = append(out, r.ib.GetName())
		}
	}
	return
}
