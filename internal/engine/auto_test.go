package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"lazy1c/internal/mmc83"
	"lazy1c/internal/ras82"
	"lazy1c/internal/ras"
)

// fakeStand — TCP-фейк ragent/RAS: шлёт приветствие (или молчит — как RAS),
// затем на каждый принятый кадр отвечает сценарием (кадры с трейлером).
// Имитирует ответы реальных серверов разных версий/протоколов.
type fakeStand struct {
	ln       net.Listener
	greeting []byte // nil = молчать (RAS-сервер)
	replies  [][]byte
	n        int
}

func startFake(t *testing.T, greeting []byte, replies [][]byte) string {
	t.Helper()
	f := &fakeStand{greeting: greeting, replies: replies}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func (f *fakeStand) serve(c net.Conn) {
	defer c.Close()
	if f.greeting != nil {
		_, _ = c.Write(f.greeting)
	}
	buf := make([]byte, 4096)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
		if len(f.replies) > 0 {
			_, _ = c.Write(f.replies[f.n%len(f.replies)])
			f.n++
		}
		// replies == nil: только приветствие и молчание
		if len(f.replies) == 0 {
			time.Sleep(50 * time.Millisecond) // держим соединение, ничего не отвечая
		}
	}
}

func frame(body string) []byte {
	return append([]byte(body), 0x66, 0x53, 0xb2, 0xa6)
}

var greet = []byte{0x53, 0xf5, 0xc6, 0x1a, 0x7b}

// TestAutoDetect8.2: ragent-приветствие + auth без «8.3.» → движок ras82.
func TestAutoDetect82(t *testing.T) {
	addr := startFake(t, greet, [][]byte{
		frame("\xef\xbb\xbfjunk"), // конверт-ответ
		frame("\xef\xbb\xbfjunk"), // challenge
		frame("\xef\xbb\xbf{2,x,\r\n{x,\"auth без версий\","), // auth — не 8.3
	})
	a := &connAuto{address: addr, timeout: 2 * time.Second}
	got := a.pick()
	if _, ok := got.(*ras82.Conn82); !ok {
		t.Fatalf("ожидался движок ras82, получен %T", got)
	}
}

// TestAutoDetect83MMC: ragent-приветствие + auth с «(8.2.19.130 - 8.3.x)»
// → движок mmc83.
func TestAutoDetect83MMC(t *testing.T) {
	addr := startFake(t, greet, [][]byte{
		frame("\xef\xbb\xbfjunk"),
		frame("\xef\xbb\xbfjunk"),
		frame("\xef\xbb\xbf{2,x,\r\n{x,\"Различаются версии клиента и сервера (8.2.19.130 - 8.3.27.2130), клиентское приложение: Консоль кластера\","),
	})
	a := &connAuto{address: addr, timeout: 2 * time.Second}
	got := a.pick()
	if _, ok := got.(*mmc83.Conn83); !ok {
		t.Fatalf("ожидался движок mmc83, получен %T", got)
	}
}

// TestAutoDetectRAS: тишина после connect (RAS молчит до negotiate) → ras.
func TestAutoDetectRAS(t *testing.T) {
	addr := startFake(t, nil, nil)
	a := &connAuto{address: addr, timeout: 2 * time.Second}
	got := a.pick()
	if _, ok := got.(*ras.Conn); !ok {
		t.Fatalf("ожидался движок ras, получен %T", got)
	}
}

// TestAutoRASDownFallback: RAS-адрес закрыт, на :1540 того же хоста пусто —
// Poll возвращает исходную ошибку ras ( mmc-фолбэк не преуспел), без паники.
func TestAutoRASDownFallback(t *testing.T) {
	// свободный порт без слушателя
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()

	a := &connAuto{address: dead, timeout: 500 * time.Millisecond}
	_, err := a.Poll(context.Background(), ras.Creds{}, ras.Creds{}, nil)
	if err == nil {
		t.Fatal("ожидали ошибку (порт закрыт), получили успех")
	}
}
