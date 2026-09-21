// Package engine — выбор движка соединения с кластером 1С: RAS (8.3+) или
// MMC-протокол ragent (8.2). Общий интерфейс позволяет TUI, dump и ctl
// работать с любой версией платформы одинаково.
package engine

import (
	"context"
	"net"
	"strings"
	"time"

	"lazy1c/internal/mmc83"
	"lazy1c/internal/ras"
	"lazy1c/internal/ras82"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// Conn — общее соединение с кластером: RAS 8.3+ или ragent 8.2.
type Conn interface {
	Poll(ctx context.Context, creds, ibCreds ras.Creds, perBase map[string]ras.Creds) (*ras.Snapshot, error)
	AgentVersion(ctx context.Context) (string, error)
	GetInfobase(ctx context.Context, clusterID, infobaseID string) (*serializev1.InfobaseInfo, error)
	GetInfobaseAuth(ctx context.Context, clusterID, infobaseID string, creds ras.Creds) (*serializev1.InfobaseInfo, error)
	UpdateInfobase(ctx context.Context, req *messagesv1.UpdateInfobaseRequest) error
	TerminateSession(ctx context.Context, clusterID, sessionID, msg string) error
	DisconnectConnection(ctx context.Context, clusterID, processID, connectionID string) error
}

// New выбирает движок: "ras" (8.3+ через RAS), "82" (ragent 8.2),
// "83" (ragent 8.3 без RAS) или "auto" — по приветствию ragent.
// Адрес из одного имени сервера дополняется стандартным портом ragent 1540.
func New(address string, timeout time.Duration, engineMode string) Conn {
	if !strings.Contains(address, ":") {
		address += ":1540"
	}
	switch engineMode {
	case "82":
		return ras82.NewConn82(address, timeout)
	case "83":
		return mmc83.NewConn83(address, timeout) // ragent 8.3 без RAS-службы
	case "ras":
		return ras.NewConn(address, timeout)
	}
	// auto: логика «ищем 8.3: сначала RAS, если его нет — MMC 8.3».
	// Быстрый probe: ragent сам здоровается, RAS молчит до negotiate.
	// За ragent-приветствием уточняем линию: 8.2-конверт против 8.3-сервера
	// отвечает «Различаются версии (8.2.19.130 - 8.3…)» → MMC 8.3.
	return &connAuto{address: address, timeout: timeout}
}

// connAuto — ленивый выбор движка при первом обращении (не в New, чтобы
// конструктор не ходил в сеть).
type connAuto struct {
	address string
	timeout time.Duration
	chosen  Conn
	viaMMC  bool      // работаем через MMC-фолбэк (RAS был недоступен)
	lastRAS time.Time // последняя проверка «ожил ли RAS»
}

func (a *connAuto) pick() Conn {
	if a.chosen != nil {
		return a.chosen
	}
	probe := a.timeout
	if probe > 2*time.Second {
		probe = 2 * time.Second
	}
	if ras82.IsRagent82(a.address, probe) {
		// ragent: 8.2 или 8.3? проба 8.2-рукопожатием: отказ с «8.3.» → MMC
		if cl, _, err := ras82.DialAuto(a.address, probe); err != nil {
			if strings.Contains(err.Error(), "8.3.") && !strings.Contains(err.Error(), "8.2.19.130 - 8.2") {
				a.chosen = mmc83.NewConn83(a.address, a.timeout)
				return a.chosen
			}
		} else {
			cl.Close()
		}
		a.chosen = ras82.NewConn82(a.address, a.timeout)
		return a.chosen
	}
	// RAS-сервер? обычный RAS-клиент; если не отвечает — MMC 8.3 на :1540
	// того же хоста (RAS-служба не поднята, а ragent есть)
	a.chosen = ras.NewConn(a.address, a.timeout)
	return a.chosen
}

func (a *connAuto) Poll(ctx context.Context, creds, ibCreds ras.Creds, perBase map[string]ras.Creds) (*ras.Snapshot, error) {
	// работаем через MMC-фолбэк — периодически (раз в 30 с) щупаем RAS:
	// подняли службу — возвращаемся на полноценный RAS-путь без перезапуска
	if a.viaMMC && time.Since(a.lastRAS) > 30*time.Second {
		a.lastRAS = time.Now()
		if tcpAlive(a.address, 2*time.Second) {
			rasConn := ras.NewConn(a.address, a.timeout)
			if snap2, err2 := rasConn.Poll(ctx, creds, ibCreds, perBase); err2 == nil {
				a.chosen = rasConn
				a.viaMMC = false
				return snap2, nil
			}
		}
	}
	c := a.pick()
	snap, err := c.Poll(ctx, creds, ibCreds, perBase)
	if err != nil {
		// RAS не поднялся → MMC 8.3 на стандартном порту ragent того же хоста
		if _, isRas := c.(*ras.Conn); isRas {
			host := a.address
			if i := strings.LastIndex(a.address, ":"); i > 0 {
				host = a.address[:i]
			}
			mmc := mmc83.NewConn83(host+":1540", a.timeout)
			if snap2, err2 := mmc.Poll(ctx, creds, ibCreds, perBase); err2 == nil {
				a.chosen = mmc
				return snap2, nil
			}
		}
	}
	return snap, err
}

// tcpAlive — слушает ли порт (RAS молчит до negotiate, поэтому достаточно
// успешного connect: refused = службы нет).
func tcpAlive(addr string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (a *connAuto) AgentVersion(ctx context.Context) (string, error) { return a.pick().AgentVersion(ctx) }
func (a *connAuto) GetInfobase(ctx context.Context, c, i string) (*serializev1.InfobaseInfo, error) {
	return a.pick().GetInfobase(ctx, c, i)
}
func (a *connAuto) GetInfobaseAuth(ctx context.Context, c, i string, cr ras.Creds) (*serializev1.InfobaseInfo, error) {
	return a.pick().GetInfobaseAuth(ctx, c, i, cr)
}
func (a *connAuto) UpdateInfobase(ctx context.Context, r *messagesv1.UpdateInfobaseRequest) error {
	return a.pick().UpdateInfobase(ctx, r)
}
func (a *connAuto) TerminateSession(ctx context.Context, c, s, m string) error {
	return a.pick().TerminateSession(ctx, c, s, m)
}
func (a *connAuto) DisconnectConnection(ctx context.Context, c, p, i string) error {
	return a.pick().DisconnectConnection(ctx, c, p, i)
}

// Kind — человекочитаемый тип движка соединения (для окна реквизитов кластера).
func Kind(c Conn) string {
	switch v := c.(type) {
	case *connAuto:
		if v.chosen != nil {
			return "авто → " + Kind(v.chosen)
		}
		return "авто"
	case *ras.Conn:
		return "RAS (8.3+)"
	case *ras82.Conn82:
		return "MMC 8.2"
	case *mmc83.Conn83:
		return "MMC 8.3"
	}
	return "?"
}
