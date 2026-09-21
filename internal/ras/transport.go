// Package ras — нативный клиент протокола RAS 1С:Предприятие (TCP, порт 1545).
// Транспорт и handshake собственные, контракты сообщений — github.com/v8platform/protos.
package ras

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	clientv1 "github.com/v8platform/protos/gen/ras/client/v1"
	protocolv1 "github.com/v8platform/protos/gen/ras/protocol/v1"
)

// connChannel кадрирует пакеты RAS поверх net.Conn.
type connChannel struct {
	mu sync.Mutex
	c  net.Conn
}

func (ch *connChannel) SendMsg(ctx context.Context, msg interface{}, _ ...interface{}) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = ch.c.SetWriteDeadline(dl)
	}
	switch m := msg.(type) {
	case *protocolv1.Packet:
		_, err := m.WriteTo(ch.c)
		return err
	case io.Reader: // NegotiateMessage уходит сырыми байтами, без кадра
		_, err := io.Copy(ch.c, m)
		return err
	}
	return fmt.Errorf("неотправляемый тип %T", msg)
}

func (ch *connChannel) RecvMsg(ctx context.Context, msg interface{}, _ ...interface{}) error {
	p, ok := msg.(*protocolv1.Packet)
	if !ok {
		return fmt.Errorf("неприёмаемый тип %T", msg)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = ch.c.SetReadDeadline(dl)
	}
	_, err := p.ReadFrom(ch.c)
	return err
}

// endpoint — открытый сервисный эндпоинт RAS (v8.service.Admin.Cluster).
type endpoint struct{ id, version int32 }

func (e *endpoint) GetId() int32      { return e.id }
func (e *endpoint) GetVersion() int32 { return e.version }

// Conn — одно соединение с RAS с ленивым (пере)подключением.
// Все вызовы сериализованы: протокол RAS строго запрос/ответ.
type Conn struct {
	addr    string
	timeout time.Duration

	mu       sync.Mutex
	ch       *connChannel
	ep       *endpoint
	svcs     services
	authDone map[string]bool // cluster uuid -> авторизован на этом соединении
}

// services — набор сервисов RAS поверх одного соединения.
type services struct {
	clusters    clientv1.ClustersService
	infobases   clientv1.InfobasesService
	sessions    clientv1.SessionsService
	locks       clientv1.LocksService
	connections clientv1.ConnectionsService
	auth        clientv1.AuthService
	admin       clientv1.AdminService
}

// invokeClient адаптирует *Conn к интерфейсу clientv1.Client.
type invokeClient struct{ conn *Conn }

func (ic *invokeClient) Invoke(ctx context.Context, needEndpoint bool, req interface{}, handler clientv1.InvokeHandler, _ ...interface{}) (interface{}, error) {
	c := ic.conn
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureConnectedLocked(ctx); err != nil {
		return nil, err
	}
	var ep clientv1.Endpoint
	if needEndpoint {
		if c.ep == nil {
			return nil, fmt.Errorf("endpoint не открыт")
		}
		ep = c.ep
	}
	reply, err := handler(ctx, c.ch, ep, req, nil)
	if err != nil && isNetError(err) {
		c.closeLocked() // следующий вызов переподключится
	}
	return reply, err
}

// NewConn создаёт соединение; подключение ленивое, при первом вызове.
func NewConn(addr string, timeout time.Duration) *Conn {
	c := &Conn{addr: addr, timeout: timeout, authDone: map[string]bool{}}
	ic := &invokeClient{conn: c}
	c.svcs = services{
		clusters:    clientv1.NewClustersService(ic),
		infobases:   clientv1.NewInfobasesService(ic),
		sessions:    clientv1.NewSessionsService(ic),
		locks:       clientv1.NewLocksService(ic),
		connections: clientv1.NewConnectionsService(ic),
		auth:        clientv1.NewAuthService(ic),
		admin:       clientv1.NewAdminService(ic),
	}
	return c
}

// Addr возвращает адрес соединения.
func (c *Conn) Addr() string { return c.addr }

// Close разрывает соединение.
func (c *Conn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *Conn) closeLocked() {
	if c.ch != nil {
		_ = c.ch.c.Close()
		c.ch = nil
	}
	c.ep = nil
	c.authDone = map[string]bool{}
}

func (c *Conn) ensureConnectedLocked(ctx context.Context) error {
	if c.ch != nil {
		return nil
	}
	d := net.Dialer{Timeout: c.timeout}
	netConn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("подключение к %s: %w", c.addr, err)
	}
	c.ch = &connChannel{c: netConn}
	if err := c.handshakeLocked(); err != nil {
		c.closeLocked()
		return err
	}
	return nil
}

// handshakeLocked вызывается с уже установленным TCP-соединением и взятом мьютексе;
// работает напрямую протокольными хендлерами, минуя invokeClient (иначе дедлок).
func (c *Conn) handshakeLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	ch := c.ch

	if err := protocolv1.SendPacketMsg(ctx, ch, protocolv1.NewNegotiateMessage()); err != nil {
		return fmt.Errorf("negotiate: %w", err)
	}
	connectAck := &protocolv1.ConnectMessageAck{}
	if err := protocolv1.PacketChannelRequest(ctx, ch, &protocolv1.ConnectMessage{}, connectAck); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	open := func(version string) (*protocolv1.EndpointOpenAck, error) {
		ack := &protocolv1.EndpointOpenAck{}
		err := protocolv1.PacketChannelRequest(ctx, ch,
			&protocolv1.EndpointOpen{Service: "v8.service.Admin.Cluster", Version: version}, ack)
		return ack, err
	}
	ack, err := open("10.0")
	if err != nil {
		ver := clientv1.DetectSupportedVersion(err)
		if ver == "" {
			return fmt.Errorf("endpoint open: %w", err)
		}
		ack, err = open(ver)
		if err != nil {
			return fmt.Errorf("endpoint open(%s): %w", ver, err)
		}
	}
	c.ep = &endpoint{id: ack.GetEndpointId(), version: parseVersion(ack.GetVersion())}
	return nil
}

func parseVersion(v string) int32 {
	major, _, _ := strings.Cut(v, ".")
	n, _ := strconv.Atoi(major)
	return int32(n)
}

// callCtx подготавливает контекст с дедлайном для публичных методов.
func (c *Conn) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func isNetError(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "EOF")
}
