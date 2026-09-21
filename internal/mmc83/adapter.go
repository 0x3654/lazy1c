// Адаптер mmc83 → engine.Conn: кластер 8.3 через ragent:1540 без RAS.
package mmc83

import (
	"context"
	"fmt"
	"time"

	"lazy1c/internal/ras"

	"google.golang.org/protobuf/types/known/timestamppb"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// Conn83 — соединение-по-требованию: каждый Poll открывает свежую сессию
// ragent (реплей словарных кадров требует исходной последовательности).
type Conn83 struct {
	addr     string
	timeout  time.Duration
	sessBase map[string]string // uuid сеанса → uuid базы (последний Poll)
	sessUser map[string]string // uuid сеанса → uuid пользователя (для terminate)
}

// NewConn83 создаёт 8.3-MMC-соединение (ленивое).
func NewConn83(addr string, timeout time.Duration) *Conn83 {
	return &Conn83{addr: addr, timeout: timeout}
}

// Poll опрашивает: кластеры + базы + сеансы (одним соединением).
func (c *Conn83) Poll(ctx context.Context, _, _ ras.Creds, _ map[string]ras.Creds) (*ras.Snapshot, error) {
	t0 := time.Now()
	cl, err := Dial(c.addr, c.timeout)
	if err != nil {
		return nil, err
	}
	defer cl.Close()

	cls, err := cl.Clusters()
	if err != nil {
		return nil, err
	}
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
	if len(cls) == 0 {
		return nil, fmt.Errorf("кластеры не найдены в ответе ragent")
	}
	for _, k := range cls {
		name := k.Name
		if name == "" {
			name = "Локальный кластер"
		}
		host := k.Host
		if host == "" {
			host = hostOf(c.addr)
		}
		cid := k.Uuid
		snap.Clusters = append(snap.Clusters, &serializev1.ClusterInfo{
			Uuid: cid, Name: name, Host: host, Port: int32(k.Port),
		})
	}
	cid := snap.Clusters[0].GetUuid()
	bases, err := cl.Bases()
	if err != nil {
		snap.ListErrs["infobases"] = err // базы не критичны: кластер уже есть
	} else {
		for _, b := range bases {
			u := b.Uuid
			if u == "" {
				u = NameUUID83(b.Name) // фолбэк: базы без uuid в списке
			}
			snap.Infobases[cid] = append(snap.Infobases[cid], &serializev1.InfobaseSummaryInfo{
				Uuid: u, Name: b.Name,
			})
		}
	}
	sess, err := cl.AllSessions()
	if err != nil {
		snap.ListErrs["sessions"] = err
	} else {
		c.sessBase = map[string]string{}
		c.sessUser = map[string]string{}
		for _, s := range sess {
			si := &serializev1.SessionInfo{
				InfobaseId: ibUuid(bases, s.InfobaseName), // uuid только из списка: в записи может быть иной
				UserName:   s.User,
				Host:       s.Host,
				AppId:      s.App,
			}
			if s.Uuid != "" {
				si.Uuid = s.Uuid
				if bu := ibUuid(bases, s.InfobaseName); bu != "" {
					c.sessBase[s.Uuid] = bu
				}
				if s.UserUUID != "" {
					c.sessUser[s.Uuid] = s.UserUUID
				}
			}
			if s.Started > 0 {
				si.StartedAt = timestamppb.New(time.Unix(s.Started/10-62135596800, 0))
			}
			if s.LastActive > 0 {
				si.LastActiveAt = timestamppb.New(time.Unix(s.LastActive/10-62135596800, 0))
			}
			snap.Sessions[cid] = append(snap.Sessions[cid], si)
		}
	}
	snap.Took = time.Since(t0)
	return snap, nil
}

// AgentVersion — версия платформы (пока без разбора из ответов; см. PROTOCOL.md).
func (c *Conn83) AgentVersion(_ context.Context) (string, error) {
	return "8.3 (MMC)", nil
}

// GetInfobase — карточка базы: в прототипе недоступна.
func (c *Conn83) GetInfobase(_ context.Context, _, _ string) (*serializev1.InfobaseInfo, error) {
	return nil, fmt.Errorf("карточка базы недоступна в прототипе 8.3-MMC")
}

// GetInfobaseAuth — недоступно в прототипе.
func (c *Conn83) GetInfobaseAuth(_ context.Context, _, _ string, _ ras.Creds) (*serializev1.InfobaseInfo, error) {
	return nil, fmt.Errorf("карточка базы недоступна в прототипе 8.3-MMC")
}

// UpdateInfobase — недоступно в прототипе (мутация — toggle, см. PROTOCOL.md).
func (c *Conn83) UpdateInfobase(_ context.Context, _ *messagesv1.UpdateInfobaseRequest) error {
	return fmt.Errorf("изменение свойств недоступно в прототипе 8.3-MMC")
}

// TerminateSession — завершение сеанса: реплей кила консоли (кадр 248Б).
// Как и в 8.2, кадр ссылается на потоковый словарь соединения: прежде чем
// слать его, прогоняем цепочку кадров (кластеры → базы → полный список
// сеансов) — без неё ragent отвергает кадр голым трейлером (4Б). Свежий
// список заодно даёт uuid базы жертвы. Сообщение игнорируем: в кадре
// остаётся реплейное «Сеанс работы завершен администратором.» — своя
// пересборка строки ломала кил (см. PROTOCOL.md).
func (c *Conn83) TerminateSession(_ context.Context, _, sessionID, _ string) error {
	cl, err := Dial(c.addr, c.timeout)
	if err != nil {
		return err
	}
	defer cl.Close()
	if _, err := cl.Clusters(); err != nil {
		return err
	}
	bases, err := cl.Bases()
	if err != nil {
		return err
	}
	all, err := cl.AllSessions()
	if err != nil {
		return err
	}
	baseUuid := c.sessBase[sessionID]
	if baseUuid == "" {
		for _, s := range all {
			if s.Uuid == sessionID {
				baseUuid = ibUuid(bases, s.InfobaseName)
				break
			}
		}
	}
	if baseUuid == "" {
		return fmt.Errorf("не найдена база сеанса %s", sessionID)
	}
	return cl.Terminate(baseUuid, sessionID)
}

// DisconnectConnection — недоступно в прототипе.
func (c *Conn83) DisconnectConnection(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("разрыв соединения недоступен в прототипе 8.3-MMC")
}

// NameUUID83 — детерминированный uuid от имени (та же схема, что ras82,
// соль своя: базы 8.3 не пересекаются с 8.2 в одном конфиге).
func NameUUID83(name string) string { return nameUUID(name) }

// ibUuid — uuid базы по имени из списка (в записи сеанса встречается иной
// uuid-объекта — для terminate он не подходит).
func ibUuid(bases []Base, name string) string {
	for _, b := range bases {
		if b.Name == name {
			return b.Uuid
		}
	}
	return NameUUID83(name)
}

func hostOf(addr string) string {
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
