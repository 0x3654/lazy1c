// Адаптер ras82 → модели TUI: собирает ras.Snapshot в тех же типах
// serializev1, что и RAS-клиент 8.3, поэтому интерфейс TUI не меняется.
package ras82

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"lazy1c/internal/ras"

	"google.golang.org/protobuf/types/known/timestamppb"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// Conn82 — долгоживущее соединение-по-требованию: каждый Poll открывает
// свежую сессию ragent (рукопожатие ~15 мс), как rac в режиме разовых команд.
// domain запоминается после первого удачного опроса (кластеры с доменной
// аутентификацией сразу пинают анонимный NTLM).
type Conn82 struct {
	addr       string
	timeout    time.Duration
	version    string            // из последнего Poll (строка 8.2.19.xxx в ответах)
	userBySess map[string]string // session uuid → user uuid (для terminate)
	ibIDs      map[string]string // имя базы → uuid, липкий на соединение
	cl         *Client           // персистентное соединение (пере-dial при обрыве)
	fs         *frameSet         // выбранный набор кадров сервера
}

// NewConn82 создаёт 8.2-соединение (ленивое, подключение при первом опросе).
func NewConn82(addr string, timeout time.Duration) *Conn82 {
	return &Conn82{addr: addr, timeout: timeout}
}

// Addr возвращает адрес соединения.
func (c *Conn82) Addr() string { return c.addr }

// Poll опрашивает 8.2-кластер и собирает снапшот в типах serializev1.
func (c *Conn82) Poll(ctx context.Context, _, _ ras.Creds, _ map[string]ras.Creds) (*ras.Snapshot, error) {
	t0 := time.Now()
	// персистентное соединение: опрос сам является активностью (keepalive не нужен);
	// при обрыве/ошибке — полный пере-dial с подбором набора кадров
	var lists *Lists
	if c.cl != nil && c.fs != nil {
		var err error
		if lists, err = c.cl.FetchQuick(c.fs, c.cl.LastClusters()); err != nil {
			c.cl.Close()
			c.cl, c.fs = nil, nil // обрыв — следующий Poll сделает полный dial
		}
	}
	if c.cl == nil {
		cl, l, err := DialAuto(c.addr, c.timeout)
		if err != nil {
			return nil, err
		}
		c.cl, c.fs, lists = cl, c.usedFrames(cl), l
	}
	if v := c.cl.Version(); v != "" {
		c.version = v
	}
	snap, err := c.collect(c.cl, lists)
	if err != nil {
		c.cl.Close()
		c.cl = nil
		return nil, err
	}
	snap.Took = time.Since(t0)
	return snap, nil
}

// usedFrames угадывает выбранный набор по факту успеха DialAuto.
func (c *Conn82) usedFrames(cl *Client) *frameSet { return cl.usedSet }

// collect собирает снапшот из готовых списков упорядоченного опроса.
func (c *Conn82) collect(cl *Client, lists *Lists) (*ras.Snapshot, error) {
	snap := &ras.Snapshot{
		At:          time.Now(),
		Infobases:   map[string][]*serializev1.InfobaseSummaryInfo{},
		Sessions:    map[string][]*serializev1.SessionInfo{},
		Connections: map[string][]*serializev1.ConnectionInfo{},
		Processes:   map[string][]*serializev1.ProcessInfo{},
		Managers:    map[string][]*serializev1.ManagerInfo{},
		Servers:     map[string][]*serializev1.ServerInfo{},
		Locks:       map[string][]*serializev1.LockInfo{},
		IBInfo:      map[string]map[string]*serializev1.InfobaseInfo{},
		ListErrs:    map[string]error{},
	}

	// кластеры: [uuid-контекст][uuid-кластера] группой подряд, затем [имя?][хост][пид];
	// uuid кластера — последний в группе; имени может не быть (тогда первая строка — хост)
	vals := lists.Clusters
	cid := ""
	var uuidGroup []string
	var pendName string
	flushCluster := func(host, name string) {
		if len(uuidGroup) == 0 {
			return
		}
		if name == "" {
			name = host // безымянный кластер показываем именем-хостом
		}
		snap.Clusters = append(snap.Clusters, &serializev1.ClusterInfo{
			Uuid: uuidGroup[len(uuidGroup)-1], Port: 1541, Host: host, Name: name,
		})
		if cid == "" {
			cid = uuidGroup[len(uuidGroup)-1]
		}
		uuidGroup = nil
	}
	for _, v := range vals {
		switch v.Kind {
		case "uuid":
			if pendName != "" { // предыдущая группа закрылась одной строкой — это был хост
				flushCluster(pendName, "")
				pendName = ""
			}
			uuidGroup = append(uuidGroup, v.Text)
		case "str", "str16":
			if isNumeric(v.Text) || len(uuidGroup) == 0 {
				continue // пид/порт или мусор между записями
			}
			if pendName == "" {
				pendName = v.Text // имя, если строка одна — хост
				continue
			}
			flushCluster(v.Text, pendName) // вторая строка — хост, первая была именем
			pendName = ""
		default:
			if pendName != "" && len(uuidGroup) > 0 {
				flushCluster(pendName, "")
				pendName = ""
			}
		}
	}
	if pendName != "" && len(uuidGroup) > 0 {
		flushCluster(pendName, "")
	}
	if len(snap.Clusters) == 0 {
		return nil, fmt.Errorf("кластер не найден в ответе ragent")
	}
	if host, _, err := net.SplitHostPort(c.addr); err == nil {
		for _, ci := range snap.Clusters {
			if ci.Host == "" {
				ci.Host = host // фолбэк: адрес из конфига
			}
		}
	}

	// базы: явный список + имена из сеансов. uuid базы — липкий на соединение:
	// настоящий кадр несёт не для всех баз (52/55 без), и если uuid у базы
	// «поплывёт» между опросами (NameUUID → настоящий), TUI потеряет
	// раскрытость базы и связку сеанс↔база. Первый ответ на имя — навсегда.
	if c.ibIDs == nil {
		c.ibIDs = map[string]string{}
	}
	resolve := func(name string) string {
		if name == "" {
			return ""
		}
		if id, ok := c.ibIDs[name]; ok {
			return id
		}
		id := ibID(lists, name) // настоящий uuid, иначе детерминированный от имени
		c.ibIDs[name] = id
		return id
	}
	names := map[string]bool{}
	for _, n := range lists.Infobases {
		names[n] = true
	}
	sessions := lists.Sessions
	for _, s := range sessions {
		names[s.InfobaseName] = true
		if s.Ver != "" {
			c.version = s.Ver
		}
	}
	for n := range names {
		if n == "" {
			continue
		}
		snap.Infobases[cid] = append(snap.Infobases[cid], &serializev1.InfobaseSummaryInfo{
			Uuid: resolve(n),
			Name: n,
		})
	}

	// сеансы → SessionInfo (привязка к базе по uuid: настоящему или детерминированному)
	c.userBySess = map[string]string{}
	for _, s := range sessions {
		if s.ID != "" && s.UserUUID != "" {
			c.userBySess[s.ID] = s.UserUUID
		}
		snap.Sessions[cid] = append(snap.Sessions[cid], &serializev1.SessionInfo{
			Uuid:         s.ID,
			InfobaseId:   resolve(s.InfobaseName), // тот же резолвер, что у баз
			StartedAt:    timestamppb.New(s.Started),
			LastActiveAt: timestamppb.New(s.LastActive),
			Host:         s.Host,
			UserName:     s.User,
			AppId:        s.App,
			Locale:       s.Locale,
		})
	}

	return snap, nil
}

// ibID — uuid базы: настоящий (из списка) или детерминированный от имени.
func ibID(lists *Lists, name string) string {
	if id := lists.InfobaseIDs[name]; id != "" {
		return id
	}
	return NameUUID(name)
}

// AgentVersion возвращает версию платформы из последнего опроса.
func (c *Conn82) AgentVersion(_ context.Context) (string, error) {
	if c.version == "" {
		return "8.2 (ragent)", nil
	}
	return c.version, nil
}

// GetInfobase — полная карточка базы: в 8.2-прототипе недоступна.
func (c *Conn82) GetInfobase(_ context.Context, _, _ string) (*serializev1.InfobaseInfo, error) {
	return nil, fmt.Errorf("карточка базы недоступна в прототипе 8.2")
}

// GetInfobaseAuth — карточка базы с логином: в 8.2-прототипе недоступна.
func (c *Conn82) GetInfobaseAuth(_ context.Context, _, _ string, _ ras.Creds) (*serializev1.InfobaseInfo, error) {
	return nil, fmt.Errorf("карточка базы недоступна в прототипе 8.2")
}

// UpdateInfobase — изменение свойств базы: в 8.2-прототипе недоступно.
func (c *Conn82) UpdateInfobase(_ context.Context, _ *messagesv1.UpdateInfobaseRequest) error {
	return fmt.Errorf("изменение свойств недоступно в прототипе 8.2")
}

// TerminateSession завершает сеанс: на свежем соединении строит словарь
// (FetchAll — terminate-кадр ссылается на потоковый словарь!) и шлёт кадр.
// Перебирает наборы прод/VM — кадры привязаны к серверу.
func (c *Conn82) TerminateSession(_ context.Context, _, sessionID, _ string) error {
	var lastErr error
	for _, fs := range []*frameSet{&prodFrames, &vmFrames} {
		cl, err := Dial(c.addr, c.timeout)
		if err != nil {
			return err
		}
		if _, ferr := cl.FetchAll(fs); ferr != nil {
			cl.Close()
			lastErr = ferr
			continue
		}
		err = cl.TerminateWith(fs.terminate, sessionID, c.userBySess[sessionID])
		cl.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// DisconnectConnection — в 8.2-прототипе недоступно.
func (c *Conn82) DisconnectConnection(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("разрыв соединения недоступен в прототипе 8.2")
}

// NameUUID — детерминированный uuid v5-подобный от имени (SHA-1),
// чтобы связывать сеансы и базы 8.2 без настоящих uuid баз.
func NameUUID(name string) string {
	h := sha1.Sum([]byte("1cras-82:" + name)) // соль заморожена: смена пересоздаст uuid баз 8.2
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x50 // версия 5
	b[8] = (b[8] & 0x3f) | 0x80 // вариант
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// IsRagent82 — быстрая проверка адреса: приветствие ragent 8.2.
func IsRagent82(addr string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, len(greetBytes))
	n, _ := conn.Read(buf)
	return n == len(greetBytes) && string(buf) == string(greetBytes)
}

// isNumeric — строка целиком из цифр (пиды/порты в ответах кластеров).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
