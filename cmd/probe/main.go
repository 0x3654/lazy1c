// probe — исследование протокола ragent (8.2): шлёт разные хендшейки
// и печатает ответы в hex. Использование: go run ./cmd/probe <host:port>
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"time"
)

// swpNegotiate — стартовое сообщение RAS/SWP: magic 475223888 («1CSWP»),
// protocol 256, version 256, BigEndian без кадра.
var swpNegotiate = []byte{
	0x1c, 0x53, 0x57, 0x50, // magic "1CSWP"
	0x01, 0x00, // protocol = 256
	0x01, 0x00, // version = 256
}

func main() {
	timeout := flag.Duration("t", 3*time.Second, "таймаут на чтение ответа")
	flag.Parse()
	addr := flag.Arg(0)
	if addr == "" {
		fmt.Println("использование: probe [-t 3s] <host:port>")
		return
	}

	rnd := make([]byte, 16)
	rand.Read(rnd)

	scenarios := []struct {
		name    string
		data    []byte
		chat    bool // сначала прочитать приветствие сервера, потом ответить
		readAll bool // читать до таймаута простоя, а не один Read
	}{
		{"passive (ничего не шлём)", nil, false, false},
		{"passive, читать до тишины", nil, false, true},
		{"swp-negotiate (RAS 8.3)", swpNegotiate, false, false},
		{"после приветствия: swp-negotiate", swpNegotiate, true, true},
		{"после приветствия: текст {1,0}", []byte("{1,0}\n"), true, true},
		{"после приветствия: мусор-текст", []byte("hello\n"), true, true},
		{"после приветствия: нули16", make([]byte, 16), true, true},
	}

	for _, sc := range scenarios {
		fmt.Printf("══ %s ══\n", sc.name)
		d := net.Dialer{Timeout: *timeout}
		conn, err := d.Dial("tcp", addr)
		if err != nil {
			fmt.Printf("  connect: %v\n\n", err)
			return
		}
		if sc.chat {
			greet := readAll(conn, *timeout)
			if greet.total > 0 {
				fmt.Printf("  приветствие %d байт:\n%s", greet.total, hex.Dump(greet.buf[:min(greet.total, 512)]))
			} else {
				fmt.Printf("  приветствие: %v\n", errStr(greet.err))
			}
		}
		if sc.data != nil {
			_ = conn.SetWriteDeadline(time.Now().Add(*timeout))
			if _, err := conn.Write(sc.data); err != nil {
				fmt.Printf("  write: %v\n\n", err)
				conn.Close()
				continue
			}
		}
		if sc.readAll {
			r := readAll(conn, *timeout)
			switch {
			case r.total > 0:
				fmt.Printf("  ответ %d байт:\n%s", r.total, hex.Dump(r.buf[:min(r.total, 512)]))
			case isTimeout(r.err):
				fmt.Printf("  тишина (timeout, соединение жило)\n")
			default:
				fmt.Printf("  %v (0 байт)\n", errStr(r.err))
			}
		} else {
			buf := make([]byte, 4096)
			_ = conn.SetReadDeadline(time.Now().Add(*timeout))
			n, err := conn.Read(buf)
			switch {
			case n > 0:
				fmt.Printf("  ответ %d байт (err=%v):\n%s", n, errStr(err), hex.Dump(buf[:min(n, 512)]))
			case isTimeout(err):
				fmt.Printf("  тишина (timeout, соединение жило)\n")
			default:
				fmt.Printf("  %v (0 байт)\n", errStr(err))
			}
		}
		conn.Close()
		fmt.Println()
	}
}

type readResult struct {
	total int
	buf   []byte
	err   error
}

// readAll читает до тихого таймаута (idle) и возвращает всё накопленное.
func readAll(conn net.Conn, idle time.Duration) readResult {
	res := readResult{buf: make([]byte, 0, 8192)}
	chunk := make([]byte, 4096)
	_ = conn.SetReadDeadline(time.Now().Add(idle))
	for {
		n, err := conn.Read(chunk)
		res.total += n
		if n > 0 && res.total <= 65536 {
			res.buf = append(res.buf, chunk[:n]...)
		}
		if err != nil {
			res.err = err
			if isTimeout(err) && res.total > 0 {
				res.err = nil // тихий таймаут — не ошибка
			}
			return res
		}
		_ = conn.SetReadDeadline(time.Now().Add(idle))
	}
}

func errStr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func isTimeout(err error) bool {
	te, ok := err.(interface{ Timeout() bool })
	return ok && te.Timeout()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
