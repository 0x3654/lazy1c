package ras82

import (
	"bytes"
	"net"
	"os"
	"regexp"
	"testing"
	"time"
)

// authSessRe — uuid сессии из auth-ответа (как в handshake).
var authSessRe = regexp.MustCompile(`\{1,\d+,1,\r\n\{"#",[0-9a-f-]{36},\r\n\{([0-9a-f-]{36})\}`)

// TestIntro83SameWire: гипотеза — MMC-каркас 8.2/8.3 общий, сервер отвергает
// только несовпадение строки версии в интро. Меняем версию в реплее на
// родную версию сервера и повторяем весь путь: конверт → NTLM → auth →
// сессия → цепочка кадров (словарь). Запуск: RA83TEST=host:1540 RA83VER=…
// ⚠️ ТОЛЬКО СОБСТВЕННЫЕ СТЕНДЫ (docker/VM): против живых серверов чужой
// инфраструктуры не запускать — NTLM3-реплей с чужой версией уронил ragent
// (инцидент на чужом стенде 16.09 — см. PROTOCOL.md).
func TestIntro83SameWire(t *testing.T) {
	addr := os.Getenv("RA83TEST")
	if addr == "" {
		t.Skip("живой 8.3-сервер — только с RA83TEST=host:1540")
	}
	ver := os.Getenv("RA83VER")
	if ver == "" {
		ver = "8.3.27.1688"
	}
	// версия повторяется в каждом рукопожатийном кадре: конверт, NTLM1, NTLM3
	// (анонимный и доменный) — сервер сверяет их
	patch := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"8.2.19.130"`), []byte(`"`+ver+`"`)) }
	env := patch(tmplEnv654)
	ntlm1 := patch(tmplNtlm1_474)
	ntlm3 := patch(tmplNtlm3_522)
	if os.Getenv("RA83DOMAIN") != "" {
		ntlm3 = patch(tmplNtlm3Domain) // доменная подпись NTLM3
	}
	if ver != "8.2.19.130" && (bytes.Equal(env, tmplEnv654) || bytes.Equal(ntlm1, tmplNtlm1_474)) {
		t.Fatalf("версия %s не подставлена в шаблоны", ver)
	}

	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c := &Client{conn: conn, timeout: 10 * time.Second}
	greet := make([]byte, len(greetBytes))
	if err := c.readFull(greet); err != nil {
		t.Fatalf("приветствие: %v", err)
	}
	t.Logf("приветствие: %x (совпадает с 8.2-маркером: %v)", greet, string(greet) == string(greetBytes))

	if _, err := c.send(env); err != nil {
		t.Fatalf("конверт: %v", err)
	}
	var resp []byte
	if os.Getenv("RA83NOACK") == "" {
		resp, err = c.readFrame()
		if err != nil {
			t.Fatalf("конверт-ответ: %v", err)
		}
		t.Logf("конверт-ответ %.80s", resp)
	} else {
		// гипотеза: при совпавшей версии сервер конверт «съедает» молча
		t.Log("конверт-ответ пропущен (RA83NOACK)")
	}

	if _, err := c.send(ntlm1); err != nil {
		t.Fatalf("ntlm1: %v", err)
	}
	if _, err := c.readFrame(); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	t.Log("challenge: ок")
	if _, err := c.send(ntlm3); err != nil {
		t.Fatalf("ntlm3: %v", err)
	}
	auth, err := c.readFrame()
	if err != nil {
		t.Fatalf("auth-ответ: %v", err)
	}
	t.Logf("auth-ответ %.200s", auth)
	m := authSessRe.FindSubmatch(auth)
	if m == nil {
		t.Fatal("auth-ответ без uuid сессии — версия не принята или протокол иной")
	}
	c.sess = string(m[1])
	c.sessLE = guidLE(c.sess)
	c.counter = 0x4ed4
	t.Logf("СЕССИЯ ОТКРЫТА (версия в интро: %s)", ver)

	req := []byte(string(tmplSessReq97[:6]) + c.sess + string(tmplSessReq97[42:]))
	if _, err := c.send(req); err != nil {
		t.Fatalf("запрос сессии: %v", err)
	}
	if _, err := c.readFrame(); err != nil {
		t.Fatalf("ответ на сессию: %v", err)
	}
	t.Log("сессия: ок")

	// реплей цепочки кадров (словарь) как есть
	for i, f := range vmFrames.seq {
		vals, err := c.binRequest(f.tmpl)
		if err != nil {
			t.Logf("кадр %d (%s): ОТКАЗ: %v", i, f.role, err)
			return
		}
		t.Logf("кадр %d (%s): %d значений", i, f.role, len(vals))
	}
}
