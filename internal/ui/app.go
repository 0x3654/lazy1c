// Package ui — TUI lazy1c на bubbletea: дерево кластеров/баз/сеансов,
// панель деталей, автоопрос, подтверждения деструктивных действий.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/engine"
	"lazy1c/internal/ras"
)

// clusterState — состояние одного кластера из конфига.
type clusterState struct {
	id               string // имя из конфига
	address          string
	creds            ras.Creds
	ibCreds          ras.Creds // администратор ИБ (базы со своим админом)
	conn             engine.Conn
	enabled          bool                     // опрашивать кластер (переключение на месте)
	version          string                   // версия платформы агента (для заголовка кластера)
	cluster          *serializev1.ClusterInfo // первый кластер эндпоинта
	snap             *ras.Snapshot
	lastErr          string
	updated          time.Time
	inFlight         bool
	expanded         bool
	firstSnap        bool //true до первого снапшота: восстановить состояние баз
	expandOnlyActive bool // Shift+→ уровень 1: показывать только базы с сеансами
	ibOpen           map[string]bool
}

// msg-типы bubbletea.
type tickMsg struct{}

// spinTickMsg — кадр крутилки «Завершаем сеансы» (пока идут pendingKills).
type spinTickMsg struct{}

// bulkDoneMsg — срок прошёл, статус «Сеансы завершены» убрать.
type bulkDoneMsg struct{ at time.Time }

// bulkFailMsg — срок прошёл, статус «не завершились» убрать.
type bulkFailMsg struct{ at time.Time }

// killTimeout — сколько ждём, что сеансы исчезнут из снапшота после terminate.
// Обычно первый же опрос их снимает; дольше — сервер молча не завершил
// (например, неразобранная uuid-роль у 8.3-MMC) — перестаём крутить.
const killTimeout = 15 * time.Second

type snapshotMsg struct {
	stateIdx int
	snap     *ras.Snapshot
	err      error
	version  string // версия платформы агента (при первом удачном опросе)
}

type actionDoneMsg struct {
	stateIdx int
	what     string
	err      error
	sessIDs  []string // uuid затронутых сеансов (terminate): при ошибке снять пометки
}

// ibInfoMsg — результат ленивой загрузки свойств базы.
type ibInfoMsg struct {
	clusterID, ibID string
	info            *serializev1.InfobaseInfo
	err             error
}

type model struct {
	cfg          *config.Config
	state        []clusterState
	rows         []row
	cursor       int
	treeScroll   int
	detailScroll int

	width, height int
	focus         int                 // 0 — дерево, 1 — детали
	zoom          *zoomState          // полноэкранный режим базы, nil = дерево
	confirm       *confirmState       // активное подтверждение, nil = нет
	filter        string              // активный фильтр (пустой = нет)
	filterScope   int                 // 0=кластеры, 1=базы, 2=сеансы (по курсору при /)
	filterInput   bool                // идёт ввод фильтра
	detailTab     int                 // активная вкладка панели деталей
	selKey        string              // ключ выбранной строки (сброс вкладки при смене)
	ibInfo        map[string]*ibEntry // clusterID/ibID → свойства базы (лениво)
	settings      Settings            // видимость категорий сеансов (персистится)
	menu          *menuState          // открытое меню настроек, nil = закрыто
	form          *formState          // форма логина/пароля базы, nil = закрыта
	cform         *clusterForm        // форма добавления кластера (a), nil = закрыта
	eshow         bool                 // окно реквизитов кластера (e)
	scan          *scanState          // поиск кластеров в сети, nil = закрыт
	formDismissed map[string]bool     // базы, где пользователь закрыл форму
	treeDirty     bool                // дерево меняли — записать при следующем тике
	pendingKills  map[string]string   // uuid сеанса → кластер: идёт завершение; метка живёт, пока сеанс есть в снапшоте
	marked       map[string]string   // uuid сеанса → кластер: отмечен пробелом (✓) — цель для d/B
	spinStep     int                  // кадр крутилки завершения (анимируется spinTickMsg)
	bulkActive   bool                 // идёт массовое завершение — по опустошению pendingKills покажем успех
	bulkDoneAt   time.Time            // момент успеха: статус «Сеансы завершены» висит 3 секунды
	bulkFailAt   time.Time            // момент неудачи: статус «не завершились» — тоже 3 секунды
	bulkFailN    int                  // сколько сеансов не завершилось (для статуса)
	killStarted  time.Time            // когда отправлен terminate (для тайм-аута крутилки)
	log           []string            // журнал последних команд
}

// ibEntry — кэш полной карточки информационной базы.
type ibEntry struct {
	info    *serializev1.InfobaseInfo
	errText string
	pending bool
}

type confirmState struct {
	question string
	danger   bool
	onYes    func() tea.Cmd

	// горизонтальное подтверждение «ДА / ОТМЕНА» (для особо опасных операций):
	// выбор стрелками, по умолчанию — ОТМЕНА
	buttons bool
	choice  int // 0 — ДА, 1 — ОТМЕНА
}

// askButtons — второй шаг подтверждения: горизонтальное ДА/ОТМЕНА, по умолчанию ОТМЕНА.
func (m *model) askButtons(question string, onYes func() tea.Cmd) {
	m.confirm = &confirmState{question: question, danger: true, buttons: true, choice: 1, onYes: onYes}
}

// Run запускает TUI.
func Run(ctx context.Context, cfg *config.Config) error {
	applyTheme(cfg.Theme)
	m := &model{cfg: cfg, ibInfo: map[string]*ibEntry{}, settings: loadSettings(),
		formDismissed: map[string]bool{}, pendingKills: map[string]string{}, marked: map[string]string{}}
	timeout := time.Duration(cfg.CommandTimeout) * time.Second
	for _, c := range cfg.Clusters {
		if m.settings.isRemovedCluster(c.Address) {
			continue // убран пользователем через D/Delete
		}
		m.state = append(m.state, clusterState{
			id: c.Name, address: c.Address,
			creds:   ras.Creds{User: c.User, Pwd: c.Pwd},
			ibCreds: ras.Creds{User: c.IbUser, Pwd: c.IbPwd},
			conn:    engine.New(c.Address, timeout, c.Engine),
			enabled: true, expanded: false, firstSnap: true, ibOpen: map[string]bool{},
		})
	}
	// кластеры, добавленные из UI (+), — из settings.toml
	for _, ec := range m.settings.Clusters {
		dup := false
		for i := range m.state {
			if m.state[i].address == ec.Address {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		name := ec.Name
		if name == "" {
			name = ec.Address
		}
		m.state = append(m.state, clusterState{
			id: name, address: ec.Address,
			conn:    engine.New(ec.Address, timeout, "auto"),
			enabled: true, expanded: false, firstSnap: true, ibOpen: map[string]bool{},
		})
	}
	m.applyTreeState() // дерево открывается как было закрыто в прошлый раз
	m.log = append(m.log, "старт: "+fmt.Sprintf("%d кластер(ов)", len(m.state)))
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.pollAll(), m.tick())
}

func (m *model) tick() tea.Cmd {
	d := time.Duration(m.cfg.RefreshInterval) * time.Second
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

// pollAll запускает опрос всех включённых кластеров без опроса в полёте.
func (m *model) pollAll() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.state {
		if !m.state[i].enabled || m.state[i].inFlight {
			continue
		}
		m.state[i].inFlight = true
		cmds = append(cmds, m.pollOne(i))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *model) pollOne(i int) tea.Cmd {
	st := &m.state[i]
	wantVersion := st.version == ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// сохранённые креды баз этого кластера — применяются автоматически
		perBase := map[string]ras.Creds{}
		for _, ibc := range m.settings.IB {
			if ibc.Cluster == st.id {
				perBase[ibc.Base] = ras.Creds{User: ibc.User, Pwd: ibc.Pwd}
			}
		}
		// единый дефолт ИБ перекрывает ib_user конфига
		ibCreds := st.ibCreds
		if d, ok := m.settings.ibDefault(); ok {
			ibCreds = ras.Creds{User: d.User, Pwd: d.Pwd}
		}
		snap, err := st.conn.Poll(ctx, st.creds, ibCreds, perBase)
		msg := snapshotMsg{stateIdx: i, snap: snap, err: err}
		if err == nil && wantVersion {
			// версия агента нужна один раз на соединение — для имени кластера
			if v, verr := st.conn.AgentVersion(ctx); verr == nil {
				msg.version = v
			}
		}
		return msg
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild()
		return m, nil

	case tea.KeyPressMsg:
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
		if m.scan != nil {
			return m.updateScan(keyNorm(msg.String()))
		}
		if m.cform != nil {
			return m.updateClusterForm(keyNorm(msg.String()))
		}
		if m.eshow {
			return m.updateClusterInfoWin(keyNorm(msg.String()))
		}
		if m.form != nil {
			return m.updateForm(msg)
		}
		if m.menu != nil {
			m.updateMenu(msg.String())
			return m, nil
		}
		if m.filterInput {
			return m.updateFilterInput(msg)
		}
		if m.zoom != nil {
			return m.updateZoom(msg)
		}
		return m.updateKeys(msg)

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.PasteMsg:
		// вставка из буфера (bracketed paste): понимают формы кластера —
		// «адрес:порт» раскладывается по полям; прочие контексты вставку
		// игнорируют, как и раньше
		if m.cform != nil {
			return m.applyClusterPaste(msg.Content)
		}
		return m, nil

	case tickMsg:
		if m.treeDirty {
			m.flushTreeState()
		}
		return m, tea.Batch(m.pollAll(), m.tick())

	case spinTickMsg:
		// крутилка живёт, пока есть незавершённые сеансы
		if len(m.pendingKills) > 0 {
			m.spinStep++
			if cmd := m.expireStaleKills(); cmd != nil {
				return m, cmd
			}
			return m, spinTick()
		}
		return m, nil

	case bulkFailMsg:
		// три секунды прошло — убираем статус неудачи
		if m.bulkFailAt.Equal(msg.at) {
			m.bulkFailAt = time.Time{}
		}
		return m, nil

	case bulkDoneMsg:
		// три секунды прошло — возвращаем обычную строку статуса
		if m.bulkDoneAt.Equal(msg.at) {
			m.bulkDoneAt = time.Time{}
		}
		return m, nil

	case snapshotMsg:
		if msg.stateIdx >= len(m.state) {
			return m, nil // кластер убрали из списка, пока шёл опрос
		}
		st := &m.state[msg.stateIdx]
		st.inFlight = false
		if msg.err != nil {
			st.lastErr = msg.err.Error()
			st.snap = nil
			m.pushLog(fmt.Sprintf("%s: опрос не удался: %v", st.id, msg.err))
		} else {
			st.snap = msg.snap
			st.lastErr = ""
			st.updated = time.Now()
			// снапшот обновился: пометка «(завершается)» живёт до последнего
			// момента сеанса — снимаем только для тех, кого в свежем списке
			// уже нет (и только у этого кластера; чужие метки не трогаем).
			// Отметки ✓ — та же участь: сеанс умер — отметка снимается.
			if (len(m.pendingKills) > 0 || len(m.marked) > 0) && st.cluster != nil {
				alive := map[string]bool{}
				for _, ss := range msg.snap.Sessions[st.cluster.GetUuid()] {
					alive[ss.GetUuid()] = true
				}
				for id, cl := range m.pendingKills {
					if cl == st.id && !alive[id] {
						delete(m.pendingKills, id)
					}
				}
				for id, cl := range m.marked {
					if cl == st.id && !alive[id] {
						delete(m.marked, id)
					}
				}
			}
			if msg.version != "" {
				st.version = msg.version
			}
			if len(msg.snap.Clusters) > 0 {
				st.cluster = msg.snap.Clusters[0]
			}
			// первый снапшот кластера — восстановить состояние раскрытости баз
			if st.firstSnap {
				st.firstSnap = false
				if st.cluster != nil {
					m.restoreIbState(st)
				}
			}
			// карточки всех баз — в кэш: бейджи РЗ/Вход видны у всех баз сразу
			if m.ibInfo == nil {
				m.ibInfo = map[string]*ibEntry{}
			}
			for _, ibMap := range msg.snap.IBInfo {
				for ibID, info := range ibMap {
					m.ibInfo[st.id+"/"+ibID] = &ibEntry{info: info}
				}
			}
			m.pushLog(fmt.Sprintf("%s: опрошен за %s", st.id, msg.snap.Took.Round(time.Millisecond)))
		}
		m.rebuild()
		return m, m.finishBulkIfIdle()

	case clusterRemovedMsg:
		idx := -1
		for i := range m.state {
			if m.state[i].address == msg.addr {
				idx = i
				break
			}
		}
		if idx >= 0 {
			m.removeCluster(idx, msg.addr)
		}
		return m, nil

	case scanDoneMsg:
		if m.scan == nil {
			return m, nil // отменили пока сканировалось
		}
		m.scan.running = false
		m.scan.results = msg.results
		m.scan.checked = make([]bool, len(msg.results))
		for i := range m.scan.checked {
			m.scan.checked[i] = true // по умолчанию всё выбрано
		}
		if msg.err != nil {
			m.scan.err = msg.err.Error()
		}
		m.pushLog(fmt.Sprintf("скан завершён: найдено %d", len(msg.results)))
		return m, nil

	case ibInfoMsg:
		k := msg.clusterID + "/" + msg.ibID
		if m.ibInfo == nil {
			m.ibInfo = map[string]*ibEntry{}
		}
		e := &ibEntry{}
		if msg.err != nil {
			e.errText = msg.err.Error()
			m.pushLog(fmt.Sprintf("%s: свойства базы: %v", msg.clusterID, msg.err))
		} else {
			e.info = msg.info
		}
		m.ibInfo[k] = e
		return m, nil

	case actionDoneMsg:
		// pendingKills НЕ снимаем здесь: команда выполнилась за миллисекунды,
		// пользователь не успел увидеть «(завершается)». Снимем когда придёт
		// свежий снапшот — между ними будет виден цикл опроса.
		if msg.stateIdx >= len(m.state) {
			return m, nil // кластер убрали, пока выполнялось действие
		}
		st := &m.state[msg.stateIdx]
		if msg.err != nil {
			m.pushLog(fmt.Sprintf("%s: %s — ОШИБКА: %v", st.id, msg.what, msg.err))
			// завершение не удалось — сеанс жить дальше, пометку снять
			for _, id := range msg.sessIDs {
				delete(m.pendingKills, id)
			}
			m.bulkActive = false // без успех-статуса: что-то не завершилось
		} else {
			m.pushLog(fmt.Sprintf("%s: %s — ок", st.id, msg.what))
		}
		// после переключений у базы — сбросить кэш свойств
		if strings.HasPrefix(msg.what, "deny ") {
			for k := range m.ibInfo {
				if strings.HasPrefix(k, st.id+"/") {
					delete(m.ibInfo, k)
				}
			}
		}
		st.inFlight = false
		return m, tea.Batch(m.pollOne(msg.stateIdx), m.finishBulkIfIdle())
	}
	return m, nil
}

// rebuild пересобирает строки дерева: курсор приклеен к объекту (а не индексу),
// поэтому обновления снапшота не сбрасывают его и активную вкладку.
// Вкладка сбрасывается только когда объект действительно исчез.
func (m *model) rebuild() {
	prevKey := m.selectionKey() // ключ строки под курсором ДО пересборки
	m.rows = m.buildRows()
	// вернуть курсор на тот же объект, если строки сдвинулись
	if prevKey != "" {
		if idx := m.indexOfKey(prevKey); idx >= 0 {
			m.cursor = idx
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if key := m.selectionKey(); key != m.selKey {
		m.selKey = key
		m.detailTab = 0
	}
	m.detailScroll = 0
	m.keepCursorVisible()
}

// rowKey — стабильный ключ строки (живёт между обновлениями снапшота).
func rowKey(r *row) string {
	switch r.kind {
	case rowCluster:
		return "c/" + r.clusterID
	case rowInfobase:
		return "i/" + r.clusterID + "/" + r.ib.GetUuid()
	case rowSession:
		return "s/" + r.session.GetUuid()
	case rowNoSessions:
		return "n/" + r.clusterID + "/" + r.ib.GetUuid()
	}
	return "svc"
}

// selectionKey — стабильный ключ выбранной строки.
func (m *model) selectionKey() string {
	if r := m.curRow(); r != nil {
		return rowKey(r)
	}
	return ""
}

// indexOfKey — индекс строки с данным ключом, -1 если нет.
func (m *model) indexOfKey(key string) int {
	for i := range m.rows {
		if rowKey(&m.rows[i]) == key {
			return i
		}
	}
	return -1
}

func (m *model) keepCursorVisible() {
	treeH := max(m.height-4, 1)
	if m.cursor < m.treeScroll {
		m.treeScroll = m.cursor
	}
	if m.cursor >= m.treeScroll+treeH {
		m.treeScroll = m.cursor - treeH + 1
	}
}

func (m *model) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyNorm(msg.String()) {
	case "q", "ctrl+c":
		if m.cfg.ConfirmOnQuit {
			m.confirm = &confirmState{
				question: "Выйти из lazy1c?",
				onYes:    func() tea.Cmd { return tea.Quit },
			}
			return m, nil
		}
		return m, tea.Quit
	case "1":
		m.focus = 0
	case "2":
		m.focus = 1
	case "/":
		m.filterInput = true
		if r := m.curRow(); r != nil {
			switch r.kind {
			case rowCluster:
				m.filterScope = 0
			case rowInfobase:
				m.filterScope = 1
			default:
				m.filterScope = 2
			}
		}
		return m, nil
	case "z":
		m.settings.HideIdle = !m.settings.HideIdle
		_ = m.settings.save()
		m.rebuild()
	case "v":
		m.settings.ShowFlags = !m.settings.ShowFlags
		_ = m.settings.save()
		m.rebuild()
	case "j", "down":
		if m.focus == 1 {
			m.detailScroll++
		} else if m.cursor < len(m.rows)-1 {
			m.cursor++
			m.keepCursorVisible()
		}
	case "k", "up":
		if m.focus == 1 {
			m.detailScroll = max(0, m.detailScroll-1)
		} else if m.cursor > 0 {
			m.cursor--
			m.keepCursorVisible()
		}
	case "pgdown":
		if m.focus == 1 {
			m.detailScroll += 10
		} else {
			for i := 0; i < 10 && m.cursor < len(m.rows)-1; i++ {
				m.cursor++
			}
			m.keepCursorVisible()
		}
	case "pgup":
		if m.focus == 1 {
			m.detailScroll = max(0, m.detailScroll-10)
		} else {
			for i := 0; i < 10 && m.cursor > 0; i++ {
				m.cursor--
			}
			m.keepCursorVisible()
		}
	case "g", "home":
		if m.focus == 1 {
			m.detailScroll = 0
		} else {
			m.cursor = 0
			m.keepCursorVisible()
		}
	case "G", "end":
		if m.focus == 0 {
			m.cursor = len(m.rows) - 1
			m.keepCursorVisible()
		}
	case "tab":
		m.jumpCluster(+1)
	case "shift+tab":
		m.jumpCluster(-1)
	case "enter":
		// Enter: на базе — zoom, на кластере — раскрыть, на сеансе — ничего
		if r := m.curRow(); r != nil {
			switch r.kind {
			case rowInfobase:
				m.zoom = &zoomState{clusterID: r.clusterID, ib: r.ib}
			case rowCluster:
				m.expandCurrent()
			}
		}
	case "l", "right":
		// → только раскрывает (никогда не сворачивает)
		m.expandCurrent()
	case "h", "left":
		// ← сворачивает / поднимает:
		// сеанс → база, база → кластер, раскрытый кластер → свернуть
		if r := m.curRow(); r != nil {
			switch r.kind {
			case rowSession:
				m.cursor = m.infobaseRowOf(r.clusterID, r.session.GetInfobaseId())
			case rowInfobase:
				if st := m.stateByName(r.clusterID); st != nil && st.ibOpen[r.ib.GetUuid()] {
					st.ibOpen[r.ib.GetUuid()] = false
					m.markTreeDirty()
					m.rebuild() // мгновенное обновление экрана
				} else {
					m.cursor = m.clusterRowOf(r.clusterID)
				}
			case rowCluster:
				if st := m.stateByName(r.clusterID); st != nil && st.expanded {
					st.expanded = false
					st.expandOnlyActive = false
					m.markTreeDirty()
					m.rebuild() // мгновенное обновление экрана
				}
			}
			m.keepCursorVisible()
		}
	case " ", "space":
		// пробел — по уровню строки: сеанс → отметка ✓ (курсор вниз),
		// база → отметить/снять все её видимые сеансы, кластер → пауза (как s)
		if r := m.curRow(); r != nil {
			switch r.kind {
			case rowSession:
				m.toggleMark(r.session.GetUuid(), r.clusterID)
				if m.cursor < len(m.rows)-1 {
					m.cursor++
					m.keepCursorVisible()
				}
			case rowInfobase:
				m.markBaseSessions(r)
			case rowCluster:
				m.togglePollPause()
			}
		}
	case "a":
		m.cform = newClusterForm()
		return m, nil
	case "D", "delete":
		// убрать кластер из списка — только на строке кластера и только
		// недвусмысленной клавишей (Shift+D / Delete), подтверждение как было
		if r := m.curRow(); r != nil && r.kind == rowCluster {
			return m, m.confirmRemoveCluster(r)
		}
	case "s":
		if r := m.curRow(); r != nil && r.kind == rowCluster {
			m.togglePollPause()
		}
	case "e":
		// реквизиты кластера (имя/адрес/версия/счётчики) — только на кластере
		if r := m.curRow(); r != nil && r.kind == rowCluster {
			m.eshow = true
		}
	case "f5":
		m.ibInfo = map[string]*ibEntry{} // кэш свойств тоже обновить
		return m, m.pollAll()
	case "[":
		m.cycleTab(-1)
	case "]":
		m.cycleTab(+1)
	case "r":
		if r := m.curRow(); r != nil && r.kind == rowInfobase {
			return m, m.confirmIbToggle(r, "jobs")
		}
	case "b":
		if r := m.curRow(); r != nil && r.kind == rowInfobase {
			return m, m.confirmIbToggle(r, "sessions")
		}
	case "x":
		m.menu = &menuState{kind: "settings", orig: m.settings} // esc откатит к снимку
		return m, nil
	case "B":
		menu := &menuState{kind: "bulk"}
		if len(m.marked) == 0 {
			// без отметок меню открывается, но предупреждает — и ничего не делает
			menu.notice = "нет отмеченных сеансов — пробел отмечает строки (✓)"
		}
		m.menu = menu
		return m, nil
	case "d":
		if m.denyMutations("завершение сеанса") {
			return m, nil
		}
		if len(m.marked) > 0 {
			return m, m.beginTerminateMarked()
		}
		return m, m.beginTerminate()
	case "shift+left":
		m.collapseAll()
	case "shift+right":
		m.expandAll()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}
		if len(m.marked) > 0 {
			m.marked = map[string]string{}
		}
	}
	return m, m.maybePrefetchIb()
}

// collapseAll — прогрессивное сворачивание: если есть открытые базы — закрыть их,
// иначе свернуть все кластеры (двойной shift+left сворачивает всё независимо от уровня).
// collapseAll — Shift+←: прогрессивное сворачивание в рамках кластера под курсором.
// Открытые базы → закрыть; баз нет → свернуть кластер.
// collapseAll — Shift+←: на кластере — свернуть ВСЁ (глобально);
// на базе/сеансе — свернуть кластер под курсором (локально).
func (m *model) collapseAll() {
	r := m.curRow()
	if r == nil {
		return
	}
	if r.kind == rowCluster {
		// глобально: свернуть все кластеры
		for i := range m.state {
			m.state[i].expanded = false
			m.state[i].expandOnlyActive = false
			m.state[i].ibOpen = map[string]bool{}
		}
		m.markTreeDirty()
		m.rebuild()
		return
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	if len(st.ibOpen) > 0 {
		st.ibOpen = map[string]bool{}
	} else if st.expandOnlyActive {
		st.expandOnlyActive = false // Shift+← с уровня «только с сеансами» → все базы
	} else {
		st.expanded = false
	}
	m.markTreeDirty()
	m.rebuild()
}

// expandAll — Shift+→: трёхуровневое разворачивание в рамках кластера под курсором.
// 1) свёрнут → раскрыть, показать только базы с сеансами
// 2) раскрыт с фильтром «только с сеансами» → показать все базы (включая пустые)
// 3) все базы видимы → раскрыть их до сеансов
func (m *model) expandAll() {
	r := m.curRow()
	if r == nil {
		return
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	if !st.expanded {
		// уровень 1: раскрыть кластер, только базы с сеансами
		st.expanded = true
		st.expandOnlyActive = true
	} else if st.expandOnlyActive {
		// уровень 2: все базы, включая пустые
		st.expandOnlyActive = false
	} else if st.snap != nil && st.cluster != nil {
		// уровень 3: раскрыть базы до сеансов
		for _, ib := range st.snap.Infobases[st.cluster.GetUuid()] {
			st.ibOpen[ib.GetUuid()] = true
		}
	}
	m.markTreeDirty()
	m.rebuild()
}

// updateFilterInput — ввод строки фильтра.
func (m *model) updateFilterInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterInput = false
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}
	case "enter":
		m.filterInput = false
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.scrollToTop()
		}
	default:
		// печатаемые символы добавляются к фильтру
		if s := msg.String(); len([]rune(s)) == 1 {
			m.filter += s
			m.scrollToTop()
		}
	}
	return m, nil
}

// scrollToTop — сброс прокрутки и курсора (после смены фильтра).
func (m *model) scrollToTop() {
	m.treeScroll = 0
	m.cursor = 0
	m.rebuild()
}

// simulateKey — выполнить действие как при нажатии клавиши (для меню).
func (m *model) simulateKey(key rune) {
	m2, _ := m.updateKeys(tea.KeyPressMsg{Code: key})
	_ = m2
}

// updateMouse — клики и колесо: меню, выбор строки дерева, скролл.
func (m *model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mu := msg.Mouse()
	// меню поверх всего: клики и колесо — его
	if m.menu != nil {
		r := m.menu.rect
		switch mu.Button {
		case tea.MouseWheelUp:
			m.menu.scroll -= 3
			m.menuClampScroll()
		case tea.MouseWheelDown:
			m.menu.scroll += 3
			m.menuClampScroll()
		default:
			if mu.X >= r.x && mu.X < r.x+r.w && mu.Y > r.y && mu.Y < r.y+r.h {
				line := mu.Y - r.y - 1 // внутри рамки
				if line >= 0 && line < len(m.menu.lineEntry) {
					if idx := m.menu.lineEntry[line]; idx >= 0 {
						kind, tab := m.menu.kind, m.menu.tab
						entries := m.menuEntries(kind, tab)
						if idx < len(entries) {
							m.menu = nil
							m.menuRunEntry(kind, tab, idx) // клик = применить и выполнить
						}
					}
				}
			} else {
				m.menu = nil // клик мимо — закрыть
			}
		}
		return m, nil
	}
	treeW := m.width * 38 / 100
	switch mu.Button {
	case tea.MouseWheelUp:
		if m.zoom != nil {
			m.zoom.scroll = max(0, m.zoom.scroll-3)
		} else if m.focus == 1 {
			m.detailScroll = max(0, m.detailScroll-3)
		} else {
			m.treeScroll = max(0, m.treeScroll-3)
			m.cursor = min(m.cursor, m.treeScroll+max(m.height-4, 1)-1)
		}
	case tea.MouseWheelDown:
		if m.zoom != nil {
			m.zoom.scroll += 3
		} else if m.focus == 1 {
			m.detailScroll += 3
		} else {
			m.treeScroll += 3
			m.keepCursorVisible()
			m.cursor = max(m.cursor, m.treeScroll)
		}
	default:
		if mu.Y < 1 || mu.Y >= m.height-2 {
			// клик по верхней рамке панели деталей = клик по вкладке
			if mu.Y == 0 && !m.zoomOpened() && mu.X >= treeW {
				m.focus = 1
				m.clickDetailTab(mu.X - treeW - 2) // после «╭─»
			}
			return m, nil
		}
		if m.zoom != nil {
			// таблица сеансов: рамка + шапка (5 строк)
			sessions := m.zoomSessions()
			idx := m.zoom.scroll + mu.Y - 5
			if idx >= 0 && idx < len(sessions) {
				m.zoom.cursor = idx
			}
		} else if mu.X < treeW {
			m.focus = 0
			idx := m.treeScroll + mu.Y - 1
			if idx >= 0 && idx < len(m.rows) {
				m.cursor = idx
			}
		} else {
			m.focus = 1
		}
	}
	return m, nil
}

// updateZoom — клавиши в полноэкранном режиме базы.
func (m *model) updateZoom(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sessions := m.zoomSessions()
	// список мог укоротиться (сеансы завершились) — держим курсор в границах
	if m.zoom.cursor >= len(sessions) {
		m.zoom.cursor = len(sessions) - 1
	}
	if m.zoom.cursor < 0 {
		m.zoom.cursor = 0
	}
	switch keyNorm(msg.String()) {
	case "esc", "h", "left", "backspace":
		m.zoom = nil
	case "j", "down":
		if m.zoom.cursor < len(sessions)-1 {
			m.zoom.cursor++
		}
	case "k", "up":
		if m.zoom.cursor > 0 {
			m.zoom.cursor--
		}
	case "g", "home":
		m.zoom.cursor = 0
	case "G", "end":
		m.zoom.cursor = len(sessions) - 1
	case " ", "space":
		// отметка ✓ сеанса под курсором, курсор — на строку ниже (lazygit staging)
		if len(sessions) > 0 && m.zoom.cursor < len(sessions) {
			m.toggleMark(sessions[m.zoom.cursor].GetUuid(), m.zoom.clusterID)
			if m.zoom.cursor < len(sessions)-1 {
				m.zoom.cursor++
			}
		}
	case "d":
		if len(m.marked) > 0 {
			return m, m.beginTerminateMarked()
		}
		if len(sessions) > 0 && !m.denyMutations("завершение сеанса") {
			return m, m.beginTerminateSession(m.zoom.clusterID, sessions[m.zoom.cursor])
		}
	case "f5":
		m.ibInfo = map[string]*ibEntry{} // кэш свойств тоже обновить
		return m, m.pollAll()
	}
	// держим курсор в видимой части
	listH := m.height/2 - 5
	if listH > 0 && m.zoom != nil {
		if m.zoom.cursor < m.zoom.scroll {
			m.zoom.scroll = m.zoom.cursor
		}
		if m.zoom.cursor >= m.zoom.scroll+listH {
			m.zoom.scroll = m.zoom.cursor - listH + 1
		}
	}
	return m, nil
}

// zoomOpened — открыт ли полноэкранный режим базы.
func (m *model) zoomOpened() bool { return m.zoom != nil }

// clickDetailTab — клик по вкладке в заголовке панели деталей
// (x — смещение от начала заголовка, после «╭─»).
func (m *model) clickDetailTab(x int) {
	tabs := m.detailTabs()
	if len(tabs) < 2 || m.curRow() == nil {
		return
	}
	pos := 0
	for i, t := range tabs {
		w := utf8.RuneCountInString(t)
		if x >= pos && x < pos+w {
			m.detailTab = i
			return
		}
		pos += w + 3 // « - » разделитель
	}
}

// cycleTab переключает вкладку панели деталей (как [ ] в lazydocker).
func (m *model) cycleTab(delta int) {
	tabs := m.detailTabs()
	if len(tabs) < 2 {
		return
	}
	m.detailTab = (m.detailTab + delta + len(tabs)) % len(tabs)
	// «Свойства» с ошибкой прав → форма логина/пароля базы
	if r := m.curRow(); r != nil && r.kind == rowInfobase && tabs[m.detailTab] == "Свойства" {
		m.maybeOpenForm(r, false)
	}
}

// confirmIbToggle готовит подтверждение переключения регл. заданий (jobs)
// или блокировки сеансов (sessions) у базы.
func (m *model) confirmIbToggle(r *row, what string) tea.Cmd {
	if m.denyMutations("переключение свойств базы") {
		return nil
	}
	var label, cur string
	entry := m.ibInfo[r.clusterID+"/"+r.ib.GetUuid()]
	if what == "jobs" {
		label = "запрет регламентных заданий"
		if entry != nil && entry.info != nil {
			if entry.info.GetScheduledJobsDeny() {
				cur = "сейчас: запрещены"
			} else {
				cur = "сейчас: разрешены"
			}
		}
	} else {
		label = "запрет начала сеансов"
		if entry != nil && entry.info != nil {
			if entry.info.GetSessionsDeny() {
				cur = "сейчас: запрещено"
			} else {
				cur = "сейчас: разрешено"
			}
		}
	}
	question := fmt.Sprintf("Переключить %s у базы %s?", label, r.ib.GetName())
	if cur != "" {
		question += " (" + cur + ")"
	}
	m.confirm = &confirmState{question: question, danger: true}

	idx := m.indexOfState(r.clusterID)
	ibID := r.ib.GetUuid()
	cuuid := ""
	if st := m.stateByName(r.clusterID); st.cluster != nil {
		cuuid = st.cluster.GetUuid()
	}
	m.confirm.onYes = func() tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn := m.state[idx].conn
			info, err := conn.GetInfobase(ctx, cuuid, ibID)
			if err != nil {
				return actionDoneMsg{stateIdx: idx, what: "deny " + what + " " + r.ib.GetName(), err: err}
			}
			if what == "jobs" {
				info.ScheduledJobsDeny = !info.GetScheduledJobsDeny()
			} else {
				info.SessionsDeny = !info.GetSessionsDeny()
			}
			err = conn.UpdateInfobase(ctx, &messagesv1.UpdateInfobaseRequest{ClusterId: cuuid, Info: info})
			return actionDoneMsg{stateIdx: idx, what: "deny " + what + " " + r.ib.GetName(), err: err}
		}
	}
	return nil
}

// maybePrefetchIb — ленивая загрузка свойств базы при её выборе.
func (m *model) maybePrefetchIb() tea.Cmd {
	r := m.curRow()
	if r == nil || r.kind != rowInfobase {
		return nil
	}
	k := r.clusterID + "/" + r.ib.GetUuid()
	if m.ibInfo == nil {
		m.ibInfo = map[string]*ibEntry{}
	}
	if _, ok := m.ibInfo[k]; ok {
		return nil
	}
	m.ibInfo[k] = &ibEntry{pending: true} // маркер: запрос уже ушёл
	idx := m.indexOfState(r.clusterID)
	ibID := r.ib.GetUuid()
	cuuid := ""
	if st := m.stateByName(r.clusterID); st.cluster != nil {
		cuuid = st.cluster.GetUuid()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := m.state[idx].conn.GetInfobase(ctx, cuuid, ibID)
		return ibInfoMsg{clusterID: r.clusterID, ibID: ibID, info: info, err: err}
	}
}

// confirmRemoveCluster — убрать кластер из списка мониторинга (сам сервер не трогаем).
func (m *model) confirmRemoveCluster(r *row) tea.Cmd {
	st := m.stateByName(r.clusterID)
	if st == nil {
		return nil
	}
	addr, id := st.address, st.id
	m.confirm = &confirmState{
		question: fmt.Sprintf("Убрать %s (%s) из списка? Кластер не затрагивается — просто перестаём читать.", id, addr),
	}
	m.confirm.onYes = func() tea.Cmd {
		return func() tea.Msg {
			return clusterRemovedMsg{addr: addr, id: id}
		}
	}
	return nil
}

// clusterRemovedMsg — кластер убран из списка (после подтверждения).
// Индекс не передаём: список мог измениться, ищем по адресу.
type clusterRemovedMsg struct {
	addr, id string
}

// removeCluster — вычистить кластер из состояния и настроек.
func (m *model) removeCluster(idx int, addr string) {
	if idx < 0 || idx >= len(m.state) {
		return
	}
	for k := range m.ibInfo {
		if strings.HasPrefix(k, m.state[idx].id+"/") {
			delete(m.ibInfo, k)
		}
	}
	m.state = append(m.state[:idx], m.state[idx+1:]...)
	// добавленный через UI — удалить из ExtraCluster
	kept := m.settings.Clusters[:0]
	for _, c := range m.settings.Clusters {
		if c.Address != addr {
			kept = append(kept, c)
		}
	}
	m.settings.Clusters = kept
	// из конфига — в список скрытых
	if !m.settings.isRemovedCluster(addr) {
		m.settings.Removed = append(m.settings.Removed, addr)
	}
	// состояние дерева убранного кластера не копим
	closed := m.settings.TreeClosed[:0]
	for _, v := range m.settings.TreeClosed {
		if v != addr {
			closed = append(closed, v)
		}
	}
	m.settings.TreeClosed = closed
	ibo := m.settings.TreeIbOpen[:0]
	for _, v := range m.settings.TreeIbOpen {
		if !strings.HasPrefix(v, addr+"|") {
			ibo = append(ibo, v)
		}
	}
	m.settings.TreeIbOpen = ibo
	if err := m.settings.save(); err != nil {
		m.pushLog("список не сохранён: " + err.Error())
	}
	m.pushLog("кластер " + addr + " убран из списка")
	m.rebuild()
}

// curRow — строка под курсором или nil.
func (m *model) curRow() *row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func onOff(v bool) string {
	if v {
		return "включён"
	}
	return "выключен"
}

func (m *model) jumpCluster(delta int) {
	if len(m.rows) == 0 {
		return
	}
	// найти текущий индекс кластера, затем следующий/предыдущий корень
	cur := 0
	for i := 0; i <= m.cursor && i < len(m.rows); i++ {
		if m.rows[i].kind == rowCluster {
			cur = i
		}
	}
	target := cur
	if delta > 0 {
		for i := m.cursor + 1; i < len(m.rows); i++ {
			if m.rows[i].kind == rowCluster {
				target = i
				break
			}
		}
		if target == cur { // wrap на первый
			for i := 0; i < len(m.rows); i++ {
				if m.rows[i].kind == rowCluster {
					target = i
					break
				}
			}
		}
	} else {
		for i := cur - 1; i >= 0; i-- {
			if m.rows[i].kind == rowCluster {
				target = i
				break
			}
		}
	}
	m.cursor = target
	m.keepCursorVisible()
}

// expandCurrent — раскрыть текущий узел (→ никогда не сворачивает).
func (m *model) expandCurrent() {
	r := m.curRow()
	if r == nil {
		return
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	switch r.kind {
	case rowCluster:
		if !st.expanded {
			st.expanded = true
			st.expandOnlyActive = false
		}
	case rowInfobase:
		st.ibOpen[r.ib.GetUuid()] = true
	}
	m.markTreeDirty()
	m.rebuild()
}

func (m *model) toggleNode() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	switch r.kind {
	case rowCluster:
		st.expanded = !st.expanded
		if st.expanded {
			st.expandOnlyActive = false // обычное раскрытие — все базы, без фильтра
		}
	case rowInfobase:
		u := r.ib.GetUuid()
		st.ibOpen[u] = !st.ibOpen[u]
	case rowSession:
		// → на сеансе — свернуть родительскую базу
		st.ibOpen[r.session.GetInfobaseId()] = false
	}
	m.markTreeDirty()
	m.rebuild()
	m.rebuild()
}

// beginTerminate готовит подтверждение для сеанса под курсором в дереве.
func (m *model) beginTerminate() tea.Cmd {
	r := m.curRow()
	if r == nil || r.kind != rowSession {
		return nil
	}
	return m.beginTerminateSession(r.clusterID, r.session)
}

// toggleMark — поставить/снять отметку ✓ у сеанса (пробел на строке сеанса).
func (m *model) toggleMark(uuid, clusterID string) {
	if m.marked == nil {
		m.marked = map[string]string{}
	}
	if _, ok := m.marked[uuid]; ok {
		delete(m.marked, uuid)
		return
	}
	m.marked[uuid] = clusterID
}

// markBaseSessions — пробел на базе: отметить все её видимые сеансы;
// если все уже отмечены — наоборот, снять все (как stage/unstage каталога).
func (m *model) markBaseSessions(r *row) {
	st := m.stateByName(r.clusterID)
	if st == nil || st.snap == nil || st.cluster == nil {
		return
	}
	if m.marked == nil {
		m.marked = map[string]string{}
	}
	all := true
	var visible []string
	for _, s := range st.snap.Sessions[st.cluster.GetUuid()] {
		if s.GetInfobaseId() != r.ib.GetUuid() || !m.sessionShown(s) {
			continue
		}
		visible = append(visible, s.GetUuid())
		if _, ok := m.marked[s.GetUuid()]; !ok {
			all = false
		}
	}
	for _, id := range visible {
		if all {
			delete(m.marked, id)
		} else {
			m.marked[id] = r.clusterID
		}
	}
}

// togglePollPause — вкл/выкл опрос кластера под курсором (s и пробел на кластере).
func (m *model) togglePollPause() {
	r := m.curRow()
	if r == nil || r.kind != rowCluster {
		return
	}
	st := m.stateByName(r.clusterID)
	if st == nil {
		return
	}
	st.enabled = !st.enabled
	m.settings.setPausedCluster(st.address, !st.enabled)
	if err := m.settings.save(); err != nil {
		m.pushLog("пауза не сохранена: " + err.Error())
	}
	m.pushLog(fmt.Sprintf("%s: опрос %s", st.id, onOff(st.enabled)))
}

// beginTerminateMarked — завершение всех отмеченных пробелом сеансов (✓).
// Превью со списком в подтверждении — тот же барьер, что и y/n у одиночного:
// пакетная операция собирается глазами, прежде чем ей скажешь «да».
func (m *model) beginTerminateMarked() tea.Cmd {
	if m.denyMutations("массовое завершение сеансов") {
		return nil
	}
	if len(m.marked) == 0 {
		m.pushLog("отмеченных сеансов нет — пробел отмечает строки (✓)")
		return nil
	}
	// живые отмеченные сеансы: uuid → сессия и её кластер (отметка могла
	// пережить сеанс между опросами — таких тихо пропускаем)
	type victim struct {
		stateIdx int
		session  *serializev1.SessionInfo
		base     string
	}
	var victims []victim
	for uuid, clusterID := range m.marked {
		st := m.stateByName(clusterID)
		if st == nil || st.snap == nil || st.cluster == nil {
			continue
		}
		for _, s := range st.snap.Sessions[st.cluster.GetUuid()] {
			if s.GetUuid() != uuid {
				continue
			}
			base := ""
			for _, ib := range st.snap.Infobases[st.cluster.GetUuid()] {
				if ib.GetUuid() == s.GetInfobaseId() {
					base = ib.GetName()
					break
				}
			}
			victims = append(victims, victim{stateIdx: m.indexOfState(clusterID), session: s, base: base})
			break
		}
	}
	if len(victims) == 0 {
		m.pushLog("отмеченные сеансы уже завершились — отметки сняты")
		m.marked = map[string]string{}
		return nil
	}
	// превью: до 5 строк, дальше «… и ещё K»
	var q strings.Builder
	fmt.Fprintf(&q, "Завершить %d сеанс(ов)?", len(victims))
	for i, v := range victims {
		if i == 5 {
			fmt.Fprintf(&q, "\n  … и ещё %d", len(victims)-i)
			break
		}
		user := v.session.GetUserName()
		if user == "" {
			user = "—"
		}
		line := fmt.Sprintf("\n  • %s %s@%s", appIDLabel(v.session.GetAppId()), user, v.session.GetHost())
		if v.base != "" {
			line += " · " + v.base
		}
		q.WriteString(line)
	}
	uuids := make([]string, len(victims))
	byCluster := map[int][]string{}
	for i, v := range victims {
		uuids[i] = v.session.GetUuid()
		byCluster[v.stateIdx] = append(byCluster[v.stateIdx], v.session.GetUuid())
	}
	m.confirm = &confirmState{question: q.String(), danger: true}
	m.confirm.onYes = func() tea.Cmd {
		// отметки уходят в работу — дальше ими занимается pendingKills
		for _, id := range uuids {
			delete(m.marked, id)
		}
		var cmds []tea.Cmd
		for idx, ids := range byCluster {
			clusterID := m.state[idx].id
			cuuid := ""
			if st := &m.state[idx]; st.cluster != nil {
				cuuid = st.cluster.GetUuid()
			}
			cmds = append(cmds, m.markPendingKill(clusterID, ids...))
			cmds = append(cmds, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				conn := m.state[idx].conn
				var lastErr error
				for _, id := range ids {
					if err := conn.TerminateSession(ctx, cuuid, id, "завершён из lazy1c (массово)"); err != nil {
						lastErr = err
					}
				}
				return actionDoneMsg{stateIdx: idx, what: fmt.Sprintf("отмеченные ×%d", len(ids)), err: lastErr, sessIDs: ids}
			})
		}
		m.bulkActive = true // ждём опустошения pendingKills → «Сеансы завершены»
		return tea.Batch(cmds...)
	}
	return nil
}

// markPendingKill — пометить кластер: в нём идёт завершение сеансов.
// Ключ — clusterID (стабилен), не uuid сеанса (у 8.2 меняется между опросами).
// Возвращаемая команда запускает крутилку в статусе и строках списка.
func (m *model) markPendingKill(clusterID string, uuids ...string) tea.Cmd {
	if m.pendingKills == nil {
		m.pendingKills = map[string]string{}
	}
	if len(m.pendingKills) == 0 {
		m.killStarted = time.Now() // отсчёт тайм-аута крутилки
	}
	for _, id := range uuids {
		m.pendingKills[id] = clusterID
	}
	m.pushLog(fmt.Sprintf("⏳ завершение: %d сеанс(ов)", len(uuids)))
	return spinTick()
}

// spinFrames — кадры крутилки завершения.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinTick — следующий кадр крутилки (120мс, пока живы pendingKills).
func spinTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{} })
}

// spinFrame — текущий кадр крутилки.
func (m *model) spinFrame() string { return spinFrames[m.spinStep%len(spinFrames)] }

// expireStaleKills — terminate отправлен, но сеансы живут дольше killTimeout
// (норма — снимаются первым же опросом). Дальше ждать бессмысленно: метки
// снимаем, сеансы возвращаем в отметки ✓ (можно повторить d), статус —
// «не завершились» на 3 секунды.
func (m *model) expireStaleKills() tea.Cmd {
	if len(m.pendingKills) == 0 || m.killStarted.IsZero() {
		return nil
	}
	if time.Since(m.killStarted) < killTimeout {
		return nil
	}
	n := len(m.pendingKills)
	for id, cl := range m.pendingKills {
		m.marked[id] = cl
		delete(m.pendingKills, id)
	}
	m.killStarted = time.Time{}
	m.bulkActive = false
	m.bulkFailN = n
	m.bulkFailAt = time.Now()
	at := m.bulkFailAt
	m.pushLog(fmt.Sprintf("⚠ %d сеанс(ов) не завершились за %s — отметки возвращены", n, killTimeout))
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return bulkFailMsg{at: at} })
}

// finishBulkIfIdle — массовое завершение дожило до опустошения pendingKills:
// показываем зелёный статус «Сеансы завершены» на 3 секунды. При ошибке
// bulkActive уже снят в actionDoneMsg — статуса не будет.
func (m *model) finishBulkIfIdle() tea.Cmd {
	if m.bulkActive && len(m.pendingKills) == 0 {
		m.bulkActive = false
		m.bulkDoneAt = time.Now()
		at := m.bulkDoneAt
		return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return bulkDoneMsg{at: at} })
	}
	return nil
}

// isPendingKill — сеанс в процессе завершения (по стабильному UUID).
func (m *model) isPendingKill(uuid string) bool {
	_, ok := m.pendingKills[uuid]
	return ok
}

// beginTerminateSession готовит подтверждение завершения сеанса
// (используется и из дерева, и из полноэкранного режима базы).
func (m *model) beginTerminateSession(clusterID string, s *serializev1.SessionInfo) tea.Cmd {
	user := s.GetUserName()
	if user == "" {
		user = "—"
	}
	m.confirm = &confirmState{
		question: fmt.Sprintf("Завершить сеанс %s %s@%s?",
			s.GetAppId(), user, s.GetHost()),
		danger: true,
	}
	idx := m.indexOfState(clusterID)
	sid := s.GetUuid()
	cuuid := ""
	if st := m.stateByName(clusterID); st.cluster != nil {
		cuuid = st.cluster.GetUuid()
	}
	m.confirm.onYes = func() tea.Cmd {
		spin := m.markPendingKill(clusterID, sid) // side effect: пометить + крутилка
		m.bulkActive = true
		return tea.Batch(spin, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := m.state[idx].conn.TerminateSession(ctx, cuuid, sid, "завершён из lazy1c")
			return actionDoneMsg{stateIdx: idx, what: "terminate " + shortUUID(sid), err: err, sessIDs: []string{sid}}
		})
	}
	return nil
}

func (m *model) indexOfState(id string) int {
	for i := range m.state {
		if m.state[i].id == id {
			return i
		}
	}
	return 0
}

func (m *model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keyNorm(msg.String())
	if m.confirm.buttons {
		switch key {
		case "left", "h", "tab", "shift+tab":
			m.confirm.choice = 1 - m.confirm.choice
		case "right", "l":
			m.confirm.choice = 1 - m.confirm.choice
		case "enter":
			fn := m.confirm.onYes
			choice := m.confirm.choice
			m.confirm = nil
			if choice == 0 && fn != nil {
				return m, fn()
			}
		case "y", "Y":
			fn := m.confirm.onYes
			m.confirm = nil
			if fn != nil {
				return m, fn()
			}
		case "n", "N", "esc", "q":
			m.confirm = nil
		}
		return m, nil
	}
	switch key {
	case "y", "Y", "enter":
		fn := m.confirm.onYes
		m.confirm = nil
		if fn != nil {
			return m, fn()
		}
	case "n", "N", "esc", "q":
		m.confirm = nil
	}
	return m, nil
}

func (m *model) pushLog(s string) {
	m.log = append(m.log, time.Now().Format("15:04:05")+" "+s)
	if len(m.log) > 200 {
		m.log = m.log[len(m.log)-200:]
	}
}

func (m *model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("загрузка…")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	treeW := m.width * 38 / 100
	detailW := m.width - treeW
	mainH := m.height - 1 // одна строка статуса

	var screen string
	if m.zoom != nil {
		screen = lipgloss.JoinVertical(lipgloss.Left, m.renderZoom(), m.renderStatus())
	} else {
		tree := m.renderTree(treeW, mainH)
		detail := m.renderDetail(detailW, mainH)
		body := lipgloss.JoinHorizontal(lipgloss.Top, tree, detail)
		screen = lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatus())
	}
	if m.scan != nil {
		sc := m.renderScan()
		sl := strings.Split(sc, "\n")
		sw := ansi.StringWidth(sl[0])
		sy := max(0, m.height/2-len(sl)/2)
		screen = overlay(screen, sc, (m.width-sw)/2, sy)
	}
	if m.cform != nil {
		cf := m.renderClusterForm()
		cl := strings.Split(cf, "\n")
		cw := ansi.StringWidth(cl[0])
		cy := max(0, m.height/2-len(cl)/2)
		screen = overlay(screen, cf, (m.width-cw)/2, cy)
	}
	if m.eshow {
		ef := m.renderClusterInfoWin()
		if ef != "" {
			el := strings.Split(ef, "\n")
			ew := ansi.StringWidth(el[0])
			ey := max(0, m.height/2-len(el)/2)
			screen = overlay(screen, ef, (m.width-ew)/2, ey)
		} else {
			m.eshow = false
		}
	}
	if m.form != nil {
		form := m.renderForm()
		fl := strings.Split(form, "\n")
		fw := ansi.StringWidth(fl[0])
		fy := max(0, m.height/2-len(fl)/2) // шапка не уходит за верх экрана
		screen = overlay(screen, form, (m.width-fw)/2, fy)
	}
	if m.menu != nil {
		menu, lineEntry := m.renderMenu()
		ml := strings.Split(menu, "\n")
		boxW := ansi.StringWidth(ml[0])
		// меню выше экрана — прижимаем к верху: шапка с табами важнее хвоста
		mx, my := (m.width-boxW)/2, max(0, m.height/2-len(ml)/2)
		m.menu.rect = rect{x: mx, y: my, w: boxW, h: len(ml)}
		m.menu.lineEntry = lineEntry
		screen = overlay(screen, menu, mx, my)
	}
	if m.confirm != nil {
		q := m.confirm.question
		if m.confirm.danger {
			q = styleModalDanger.Render("⚠ " + q)
		}
		footer := " y — да   n — отмена"
		if m.confirm.buttons {
			// горизонтальный выбор, по умолчанию — ОТМЕНА
			yes, no := "  ДА  ", "  ОТМЕНА  "
			if m.confirm.choice == 0 {
				yes = styleSelected.Render(yes)
			} else {
				no = styleSelected.Render(no)
			}
			footer = yes + " " + no + "\n ←→: выбор, enter: подтвердить"
		}
		modal := styleModal.Render(q + "\n\n" + footer)
		ml := strings.Split(modal, "\n")
		boxW := ansi.StringWidth(ml[0])
		screen = overlay(screen, modal, (m.width-boxW)/2, m.height/2-len(ml)/2)
	}
	// страховка вёрстки: панели не выше mainH, статус всегда последняя строка
	sl := strings.Split(screen, "\n")
	if len(sl) > m.height {
		// сохранить статус (последнюю строку), обрезать панели
		status := m.renderStatus()
		sl = sl[:m.height-1]
		sl = append(sl, status)
	}
	for i, l := range sl {
		sl[i] = ansi.Truncate(l, m.width, "")
	}
	v := tea.NewView(strings.Join(sl, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// hints — контекстные подсказки: зависят от режима (фильтр/zoom/фокус)
// и от того, какая строка дерева выбрана.
// hints — контекстные подсказки в формате lazydocker:
// действия, затем q: выход, x: menu, стрелки — в конце.
func (m *model) hints() string {
	switch {
	case m.menu != nil:
		if m.menu.kind == "bulk" {
			return "↑/↓: выбор, enter: выполнить, esc: отмена"
		}
		return "↑/↓: выбор, [: пред. таб, ]: след. таб, пробел: [x], enter: применить, esc: отмена"
	case m.scan != nil:
		return "enter: добавить выбранное, esc: отмена"
	case m.cform != nil:
		return "enter: далее/ок, f: подсети, esc: отмена · вставка из буфера: адрес:порт"
	case m.eshow:
		return "esc: закрыть"
	case m.form != nil:
		return "enter: далее/ок, esc: отмена"
	case m.filterInput:
		return "enter: применить, esc: сброс"
	case m.zoom != nil:
		if len(m.marked) > 0 {
			return fmt.Sprintf("✓ отмечено %d · d: завершить отмеченные · B: массовые · esc: назад, F5: обновить, q: выход, ← → ↑ ↓: сеанс", len(m.marked))
		}
		return "esc: назад, пробел: отметить, d: завершить, B: массовые, F5: обновить, q: выход, ← → ↑ ↓: сеанс"
	case m.focus == 1:
		return "PgUp/PgDn: прокрутка, 1: к дереву, q: выход, ← → ↑ ↓: прокрутка"
	}
	if r := m.curRow(); r != nil {
		switch r.kind {
		case rowCluster:
			return "e: реквизиты, пробел/s: вкл/выкл опроса, z: спящие, B: массовые, F5: обновить, q: выход, x: menu, ← → ↑ ↓"
		case rowInfobase:
			return "enter: база, пробел: отметить сеансы, r: регл.задания, b: блокировка, /: фильтр, q: выход, x: menu, ← → ↑ ↓: навигация"
		case rowSession:
			if len(m.marked) > 0 {
				return fmt.Sprintf("✓ отмечено %d · d: завершить отмеченные, esc: снять отметки, B: массовые, /: фильтр, q: выход, ← → ↑ ↓", len(m.marked))
			}
			return "пробел: отметить, d: завершить, B: массовые, /: фильтр, q: выход, x: menu, ← → ↑ ↓: навигация"
		}
	}
	if len(m.state) == 0 {
		return "a: добавить кластер, q: выход, x: menu"
	}
	return "/: фильтр, a: добавить кластер, 2: детали, q: выход, x: menu, ← → ↑ ↓: навигация"
}
func (m *model) renderStatus() string {
	// вместо обычной полоски подсказок: ход завершения и его итог.
	// Текст — зелёным (цвет активной рамки), висит до конца работы + 3с.
	var hints string
	switch {
	case len(m.pendingKills) > 0:
		hints = styleTabActive.Render(fmt.Sprintf(" %s Завершаем сеансы (%d)…", m.spinFrame(), len(m.pendingKills)))
	case !m.bulkFailAt.IsZero():
		hints = styleWarn.Render(fmt.Sprintf(" ⚠ %d сеанс(ов) не завершились — отметки ✓ возвращены", m.bulkFailN))
	case !m.bulkDoneAt.IsZero():
		hints = styleTabActive.Render(" ✓ Сеансы завершены")
	default:
		hints = styleHint.Render(" " + m.hints())
	}
	if m.filterInput || m.filter != "" {
		cursor := ""
		if m.filterInput {
			cursor = "▏"
		}
		hints += "   " + styleWarn.Render("фильтр: /"+m.filter+cursor)
	}
	return truncLine(hints, m.width)
}

// overlay кладёт box на экран по координатам.
// Работает по ячейкам (ansi.Cut), а не по байтам: не ломает ANSI-стили и кириллицу.
func overlay(screen, box string, x, y int) string {
	lines := strings.Split(screen, "\n")
	boxLines := strings.Split(box, "\n")
	for i, bl := range boxLines {
		yi := y + i
		if yi < 0 || yi >= len(lines) {
			continue
		}
		l := lines[yi]
		lw := ansi.StringWidth(l)
		if lw < x { // добиваем пробелами до левого края модалки
			l += strings.Repeat(" ", x-lw)
			lw = x
		}
		right := x + ansi.StringWidth(bl)
		lines[yi] = ansi.Cut(l, 0, x) + bl + ansi.Cut(l, right, lw)
	}
	return strings.Join(lines, "\n")
}

func truncLine(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}

// clusterRowOf — индекс строки кластера по его ID.
func (m *model) clusterRowOf(clusterID string) int {
	for i, r := range m.rows {
		if r.kind == rowCluster && r.clusterID == clusterID {
			return i
		}
	}
	return 0
}

// infobaseRowOf — индекс строки базы по кластеру и uuid базы.
func (m *model) infobaseRowOf(clusterID, ibID string) int {
	for i, r := range m.rows {
		if r.kind == rowInfobase && r.clusterID == clusterID && r.ib.GetUuid() == ibID {
			return i
		}
	}
	return m.clusterRowOf(clusterID) // не нашли — на кластер
}
