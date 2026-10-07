package mmc83

import (
	"encoding/binary"
	"testing"
)

// Кадры-имитации ответов сервера 8.3-MMC (формат записи — PROTOCOL.md).
const t2026 = int64(639251530000000) // валидные тики 100мкс (2026)

func str9a(s string) []byte {
	return append([]byte{0x9a, byte(len(s))}, s...)
}

func date(t int64) []byte {
	b := make([]byte, 9)
	b[0] = 0x91
	binary.LittleEndian.PutUint64(b[1:], uint64(t))
	return b
}

func uuid95() []byte {
	b := make([]byte, 17)
	b[0] = 0x95
	return b
}

func rec(name, host, user, app string, extra ...string) []byte {
	var out []byte
	out = append(out, uuid95()...) // uuid-набор перед именем (2-3 шт)
	out = append(out, uuid95()...)
	out = append(out, str9a(name)...)
	out = append(out, date(t2026)...)
	out = append(out, date(t2026)...)
	out = append(out, str9a(host)...)
	if user != "" {
		out = append(out, str9a(user)...)
	}
	out = append(out, str9a(app)...)
	for _, e := range extra {
		out = append(out, str9a(e)...)
	}
	return out
}

// TestParseSessionsVariants — реакции клиента на разные формы записей
// из ответов сервера: обычная, безымянная (экран логина), консольная
// (приложение не на 3-й позиции), лицензионный мусор (не запись).
func TestParseSessionsVariants(t *testing.T) {
	var frame []byte
	frame = append(frame, rec("erp_demo", "client.local", "Admin", "1CV8C")...)                 // обычная
	frame = append(frame, rec("trade_demo_159", "client.local", "", "1CV8C")...)                // логин-экран
	frame = append(frame, rec("trade_2782", "MD2DD", "Admin", "SrvrConsole", "server1c", "182")...) // консольная
	frame = append(frame, str9a("server1c")...)                                                 // мусор без дат
	frame = append(frame, str9a("file:///var/1C/licenses/x.lic")...)
	frame = append(frame, date(t2026)...) // одиночная дата после мусора — не запись

	got := parseSessions(frame)
	if len(got) != 3 {
		t.Fatalf("распознано %d записей, хочу 3: %+v", len(got), got)
	}
	want := []struct{ base, user, app string }{
		{"erp_demo", "Admin", "1CV8C"},
		{"trade_demo_159", "", "1CV8C"},
		{"trade_2782", "Admin", "SrvrConsole"},
	}
	for i, w := range want {
		s := got[i]
		if s.InfobaseName != w.base || s.User != w.user || s.App != w.app {
			t.Errorf("запись %d: база=%q юзер=%q прил=%q, хочу %q/%q/%q",
				i, s.InfobaseName, s.User, s.App, w.base, w.user, w.app)
		}
	}
}

// TestParseSessionsGarbageRejected — случайные строки с датами (данные
// лицензий) не должны превращаться в сеансы.
func TestParseSessionsGarbageRejected(t *testing.T) {
	var frame []byte
	frame = append(frame, str9a("500000164171")...)
	frame = append(frame, str9a("file:///lic")...)
	frame = append(frame, date(t2026)...)
	frame = append(frame, date(t2026)...)
	frame = append(frame, str9a("server1c")...)
	frame = append(frame, str9a("server1c")...)
	frame = append(frame, str9a("182")...)
	// 182 — не известное приложение → запись отброшена
	if got := parseSessions(frame); len(got) != 0 {
		t.Fatalf("мусор распознан как %d записей: %+v", len(got), got)
	}
}

// TestParseSessionsDateRange — записи с датами вне 2020–2035 (мусор парсера)
// отбрасываются.
func TestParseSessionsDateRange(t *testing.T) {
	var frame []byte
	frame = append(frame, uuid95()...)
	frame = append(frame, str9a("erp_demo")...)
	b := make([]byte, 9)
	b[0] = 0x91
	binary.LittleEndian.PutUint64(b[1:], 12345) // явно не дата
	frame = append(frame, b...)
	frame = append(frame, b...)
	frame = append(frame, str9a("h")...)
	frame = append(frame, str9a("u")...)
	frame = append(frame, str9a("1CV8C")...)
	if got := parseSessions(frame); len(got) != 0 {
		t.Fatalf("запись с мусорной датой не отброшена: %+v", got)
	}
}
