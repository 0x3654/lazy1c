package ras

import (
	"context"
	"fmt"
	"time"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// Creds — учётные данные администратора кластера (пустые = неявная авторизация,
// работает, когда у кластера не задан администратор).
type Creds struct {
	User string
	Pwd  string
}

// Snapshot — полное состояние одного RAS-эндпоинта за один опрос.
type Snapshot struct {
	At   time.Time
	Took time.Duration

	Clusters []*serializev1.ClusterInfo

	Infobases   map[string][]*serializev1.InfobaseSummaryInfo // key: cluster uuid
	Sessions    map[string][]*serializev1.SessionInfo
	Connections map[string][]*serializev1.ConnectionInfo
	Processes   map[string][]*serializev1.ProcessInfo
	Managers    map[string][]*serializev1.ManagerInfo
	Servers     map[string][]*serializev1.ServerInfo
	Locks       map[string][]*serializev1.LockInfo

	// IBInfo — полные карточки информационных баз (cluster uuid → base uuid → карточка);
	// для бейджей РЗ/Вход у всех баз дерева сразу. Ошибки прав молча пропускаются.
	IBInfo map[string]map[string]*serializev1.InfobaseInfo

	// ListErrs — ошибки отдельных списков (например, прав не хватило),
	// не фатальные: остальной снапшот валиден.
	ListErrs map[string]error
}

// Poll опрашивает всё состояние эндпоинта: кластеры + списки по каждому кластеру.
// ibCreds — администратор ИБ на кластер; perBase — сохранённые креды конкретных
// баз (ключ — имя базы 1С), применяются в первую очередь.
func (c *Conn) Poll(ctx context.Context, creds, ibCreds Creds, perBase map[string]Creds) (*Snapshot, error) {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()

	t0 := time.Now()
	snap := &Snapshot{
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

	clusters, err := c.svcs.clusters.GetClusters(ctx, &messagesv1.GetClustersRequest{})
	if err != nil {
		return nil, err // фатально: эндпоинт недоступен или handshake сломан
	}
	snap.Clusters = clusters.Clusters

	for _, cl := range clusters.Clusters {
		cid := cl.GetUuid()
		if err := c.ensureAuth(ctx, cid, creds); err != nil {
			return nil, fmt.Errorf("авторизация в кластере %s: %w", cl.GetName(), err)
		}

		if ib, err := c.svcs.infobases.GetInfobasesSummary(ctx,
			&messagesv1.GetInfobasesSummaryRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["infobases"] = err
		} else {
			snap.Infobases[cid] = ib.Infobases
		}
		if ss, err := c.svcs.sessions.GetSessions(ctx,
			&messagesv1.GetSessionsRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["sessions"] = err
		} else {
			snap.Sessions[cid] = ss.Sessions
		}
		if cn, err := c.svcs.connections.GetConnections(ctx,
			&messagesv1.GetConnectionsRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["connections"] = err
		} else {
			snap.Connections[cid] = cn.Connections
		}
		if pr, err := c.svcs.clusters.GetWorkingProcesses(ctx,
			&messagesv1.GetWorkingProcessesRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["processes"] = err
		} else {
			snap.Processes[cid] = pr.Processes
		}
		if mg, err := c.svcs.clusters.GetManagers(ctx,
			&messagesv1.GetClusterManagersRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["managers"] = err
		} else {
			snap.Managers[cid] = mg.Managers
		}
		if sv, err := c.svcs.clusters.GetWorkingServers(ctx,
			&messagesv1.GetWorkingServersRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["servers"] = err
		} else {
			snap.Servers[cid] = sv.Servers
		}
		if lk, err := c.svcs.locks.GetLocks(ctx,
			&messagesv1.GetLocksRequest{ClusterId: cid}); err != nil {
			snap.ListErrs["locks"] = err
		} else {
			snap.Locks[cid] = lk.Locks
		}

		// полные карточки баз — для бейджей РЗ/Вход у всех баз дерева.
		// База со своим администратором требует авторизации в самой ИБ
		// (ADD_AUTHENTICATION_REQUEST, как --infobase-user у rac) — пробуем
		// неявно пустыми кредами и кредами кластера, затем повторяем запрос.
		ibFull := map[string]*serializev1.InfobaseInfo{}
		for _, ib := range snap.Infobases[cid] {
			variants := []Creds{}
			if cr, ok := perBase[ib.GetName()]; ok {
				variants = append(variants, cr) // сохранённые креды этой базы — в первую очередь
			}
			variants = append(variants, Creds{}, creds, ibCreds)
			info, err := c.getInfobaseAny(ctx, cid, ib.GetUuid(), variants)
			if err == nil {
				ibFull[ib.GetUuid()] = info
			}
		}
		snap.IBInfo[cid] = ibFull
	}
	snap.Took = time.Since(t0)
	return snap, nil
}

// ensureAuth выполняет авторизацию в кластере один раз на соединение.
func (c *Conn) ensureAuth(ctx context.Context, clusterID string, creds Creds) error {
	c.mu.Lock()
	done := c.authDone[clusterID]
	c.mu.Unlock()
	if done {
		return nil
	}
	_, err := c.svcs.auth.AuthenticateCluster(ctx, &messagesv1.ClusterAuthenticateRequest{
		ClusterId: clusterID,
		User:      creds.User,
		Password:  creds.Pwd,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.authDone[clusterID] = true
	c.mu.Unlock()
	return nil
}

// TerminateSession принудительно завершает сеанс.
func (c *Conn) TerminateSession(ctx context.Context, clusterID, sessionID, msg string) error {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	_, err := c.svcs.sessions.TerminateSession(ctx, &messagesv1.TerminateSessionRequest{
		ClusterId: clusterID,
		SessionId: sessionID,
		Message:   msg,
	})
	return err
}

// DisconnectConnection разрывает соединение.
func (c *Conn) DisconnectConnection(ctx context.Context, clusterID, processID, connectionID string) error {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	_, err := c.svcs.connections.DisconnectConnection(ctx, &messagesv1.DisconnectConnectionRequest{
		ClusterId:    clusterID,
		ProcessId:    processID,
		ConnectionId: connectionID,
	})
	return err
}

// getInfobaseAny — карточка базы с лестницей кредов: прямым запросом,
// затем с авторизацией в ИБ каждым вариантом по очереди.
func (c *Conn) getInfobaseAny(ctx context.Context, clusterID, infobaseID string, variants []Creds) (*serializev1.InfobaseInfo, error) {
	info, err := c.GetInfobase(ctx, clusterID, infobaseID)
	if err == nil {
		return info, nil
	}
	for _, cr := range variants {
		if authErr := c.authenticateInfobase(ctx, clusterID, cr); authErr != nil {
			continue
		}
		if info, err = c.GetInfobase(ctx, clusterID, infobaseID); err == nil {
			return info, nil
		}
	}
	return nil, err
}

// GetInfobaseAuth — карточка базы с явной авторизацией в ИБ (форма логина).
func (c *Conn) GetInfobaseAuth(ctx context.Context, clusterID, infobaseID string, creds Creds) (*serializev1.InfobaseInfo, error) {
	if err := c.authenticateInfobase(ctx, clusterID, creds); err != nil {
		return nil, err
	}
	return c.GetInfobase(ctx, clusterID, infobaseID)
}

// authenticateInfobase — авторизация в информационных базах кластера
// (для баз со своим администратором; creds пустые = неявная).
func (c *Conn) authenticateInfobase(ctx context.Context, clusterID string, creds Creds) error {
	_, err := c.svcs.auth.AuthenticateInfobase(ctx, &messagesv1.AuthenticateInfobaseRequest{
		ClusterId: clusterID,
		User:      creds.User,
		Password:  creds.Pwd,
	})
	return err
}

// GetInfobase возвращает полную информацию о базе (после ensureAuth).
func (c *Conn) GetInfobase(ctx context.Context, clusterID, infobaseID string) (*serializev1.InfobaseInfo, error) {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := c.svcs.infobases.GetInfobase(ctx, &messagesv1.GetInfobaseInfoRequest{
		ClusterId:  clusterID,
		InfobaseId: infobaseID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Info, nil
}

// UpdateInfobase меняет свойства базы (блокировки сеансов, регл. задания и пр.).
func (c *Conn) UpdateInfobase(ctx context.Context, req *messagesv1.UpdateInfobaseRequest) error {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	_, err := c.svcs.infobases.UpdateInfobase(ctx, req)
	return err
}

// Discover — лёгкая проба для поиска: версия агента + список кластеров.
// Авторизация не нужна, мусор на порту отсекается (не-1С не ответит протоколом).
func (c *Conn) Discover(ctx context.Context) (version string, clusters []*serializev1.ClusterInfo, err error) {
	cl, err := c.svcs.clusters.GetClusters(ctx, &messagesv1.GetClustersRequest{})
	if err != nil {
		return "", nil, err
	}
	v, _ := c.AgentVersion(ctx)
	return v, cl.Clusters, nil
}

// AgentVersion возвращает версию агента кластера.
func (c *Conn) AgentVersion(ctx context.Context) (string, error) {
	ctx, cancel := c.callCtx(ctx)
	defer cancel()
	v, err := c.svcs.admin.GetVersion(ctx, &messagesv1.GetAgentVersionRequest{})
	if err != nil {
		return "", err
	}
	return v.GetVersion(), nil
}
