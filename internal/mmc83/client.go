// Package mmc83 — клиент MMC-протокола ragent 8.3 (порт 1540, без службы RAS).
// Реплей кадров, снятых с настоящей консоли кластера (см. PROTOCOL.md):
// рукопожатие одним конвертом, uuid сессии генерит клиент, дальше цепочка
// кадров со словарём. Один Poll = одно соединение (как rac в разовом режиме).
package mmc83

import (
	"os"
	"fmt"
	"net"
	"encoding/binary"
	"bytes"
	"crypto/rand"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	greet   = "\x53\xf5\xc6\x1a\x7b" // ragent здоровается первым
	frameTa = "\x66\x53\xb2\xa6"     // трейлер каждого кадра
)

// Client — одно соединение к ragent 8.3.
type Client struct {
	conn    net.Conn
	timeout time.Duration
	lastCnt uint16 // максимальный счётчик кадров (@0x13), отправленных здесь

	// пропатченные под это соединение кадры: uuid клиента/сессии/контекста
	// генерятся при каждом Dial (реплей с чужими uuid сервер отвергает
	// «Сеанс работы завершен администратором»)
	fEnv, fFinal, fClusters, fDiag, fCh95, fBases, fProps,
	fConn128, fA150, fSessions, fAllSessions, fToggle, fTerminate []byte
}

// Session — запись сеанса из бинарного ответа (эмпирический разбор).
// Порядок полей в записи: [uuid-набор] "база" 91начал 91актив "хост" "юзер" "прил" …
type Session struct {
	Uuid, UserUUID, InfobaseUUID string // uuid сеанса / юзера (может не быть) / базы
	InfobaseName, User, Host, App string
	Started, LastActive           int64 // тики 100мкс с 0001 (как в 8.2)
}

// genUUID — свежий uuid v4 (клиентские uuid обязаны быть уникальными).
func genUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// uuidLE16 — каноничный uuid → 16 байт GUID-LE.
func uuidLE16(u string) []byte {
	hx := strings.ReplaceAll(u, "-", "")
	b := make([]byte, 16)
	for i := 0; i < 16; i++ {
		var v byte
		fmt.Sscanf(hx[i*2:i*2+2], "%02x", &v)
		b[i] = v
	}
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
	b[4], b[5] = b[5], b[4]
	b[6], b[7] = b[7], b[6]
	return b
}

// Dial подключается и выполняет рукопожатие реплеем со СВОИМИ uuid.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, timeout: timeout}
	// генерится только uuid сессии (40a107f8 в захвате): остальное —
	// константы приложения/контекста, сервер проверяет их валидность
	c.patchFrames("e23134a2-14ff-4160-ba5f-ccef04e3786f",
		"7f58f27d-5ad8-43a1-aa1e-c982f41bed5c",
		"bc9113bc-5b88-4c9f-9fc0-ff7ade5450f0",
		"40a107f8-cbb1-438e-a454-bf08994714ca",
		"73cdaa77-2afc-436c-b738-58c772e3a0bf")
	g := make([]byte, len(greet))
	if err := c.readFull(g); err != nil {
		conn.Close()
		return nil, fmt.Errorf("приветствие: %w", err)
	}
	if string(g) != greet {
		conn.Close()
		return nil, fmt.Errorf("не ragent (приветствие %x)", g)
	}
	// конверт+интро → конверт-ответ; короткий финал → ack {1,id,96}
	if err := c.send(c.fEnv); err != nil {
		conn.Close()
		return nil, fmt.Errorf("конверт: %w", err)
	}
	if _, err := c.readFrame(); err != nil { // конверт-ответ (challenge внутри)
		conn.Close()
		return nil, fmt.Errorf("конверт-ответ: %w", err)
	}
	if err := c.send(c.fFinal); err != nil {
		conn.Close()
		return nil, fmt.Errorf("финал рукопожатия: %w", err)
	}
	ack, err := c.readFrame()
	if err != nil || !strings.Contains(string(ack), ",96}") {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("рукопожатие не принято: %.400s", ack)
		}
		return nil, fmt.Errorf("ack: %w", err)
	}
	return c, nil
}

// Close разрывает соединение.
func (c *Client) Close() { c.conn.Close() }

// patchFrames подставляет свежие uuid во все кадры реплея:
// клиента (e23134a2), второй-интро (7f58f27d), конверта (bc9113bc),
// сессии (40a107f8 — строкой в текстовых и LE-байтами в бинарных),
// контекста (73cdaa77 — аналогично). Длины сохраняются, словарные
// индексы не смещаются.
func (c *Client) patchFrames(uClient, uIntro2, uConv3, uSess, uCtx string) {
	sessLE, ctxLE := uuidLE16(uSess), uuidLE16(uCtx)
	oldCtxLE := uuidLE16("73cdaa77-2afc-436c-b738-58c772e3a0bf")

	// текстовые: строки-uuid (длина 36)
	txt := func(b []byte) []byte {
		b = bytes.ReplaceAll(b, []byte("e23134a2-14ff-4160-ba5f-ccef04e3786f"), []byte(uClient))
		b = bytes.ReplaceAll(b, []byte("7f58f27d-5ad8-43a1-aa1e-c982f41bed5c"), []byte(uIntro2))
		b = bytes.ReplaceAll(b, []byte("bc9113bc-5b88-4c9f-9fc0-ff7ade5450f0"), []byte(uConv3))
		b = bytes.ReplaceAll(b, []byte("40a107f8-cbb1-438e-a454-bf08994714ca"), []byte(uSess))
		b = bytes.ReplaceAll(b, []byte("73cdaa77-2afc-436c-b738-58c772e3a0bf"), []byte(uCtx))
		return b
	}
	// бинарные: guid-LE сессии в голове + контекст в теле
	bin := func(b []byte) []byte {
		b = txt(b)
		copy(b[2:18], sessLE)
		return bytes.ReplaceAll(b, oldCtxLE, ctxLE)
	}
	c.fEnv = txt(tmplEnv744)
	c.fFinal = txt(tmplFinal92)
	c.fClusters = txt(tmplClusters)
	c.fDiag = txt(tmplDiag192) // текстовый кадр: только строки, без LE-патча
	c.fCh95 = bin(tmplCh95)
	c.fBases = bin(tmplBases)
	c.fProps = bin(tmplProps)
	c.fConn128 = bin(tmplConn128)
	c.fA150 = bin(tmplA150)
	c.fSessions = bin(tmplSessions)
	c.fAllSessions = bin(tmplAllSessions)
	c.fToggle = bin(tmplToggle)
	c.fTerminate = bin(tmplTerminate)
}

func (c *Client) send(b []byte) error {
	// сервер требует монотонные счётчики кадров: запоминаем максимум
	if len(b) >= 0x15 {
		if v := counterLE(b); v > c.lastCnt {
			c.lastCnt = v
		}
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err := c.conn.Write(b)
	return err
}

func (c *Client) readFull(buf []byte) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(c.timeout))
	got := 0
	for got < len(buf) {
		n, err := c.conn.Read(buf[got:])
		got += n
		if err != nil {
			return err
		}
	}
	return nil
}

// readFrame читает один кадр до трейлера (с дренажем пушей).
func (c *Client) readFrame() ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 8192)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(c.timeout))
		n, err := c.conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if i := lastIndex(buf, frameTa); i >= 0 && i+len(frameTa) == len(buf) {
				return buf, nil
			}
		}
		if err != nil {
			if len(buf) > 0 && strings.Contains(err.Error(), "i/o timeout") {
				return buf, nil
			}
			return buf, err
		}
		if len(buf) > 1<<20 {
			return buf, fmt.Errorf("кадр слишком большой")
		}
	}
}

// sendOnly — отправка без чтения ответа (для тестов/мутаций).
func (c *Client) sendOnly(b []byte) error { return c.send(b) }

// req — бинарный запрос [41 95][guid][тело] и чтение ответа [42 8f…].
func (c *Client) req(tmpl []byte) ([]byte, error) {
	if err := c.send(tmpl); err != nil {
		return nil, err
	}
	r, err := c.readFrame()
	if err != nil && len(r) == 0 {
		return nil, err
	}
	return r, nil
}

func lastIndex(b []byte, s string) int { return strings.LastIndex(string(b), s) }

// ---- разбор ответов (эмпирические парсеры) ----

// clusterRe — текстовый ответ списка кластеров (имя и хост-порт во вложенных кортежах)
var clusterRe = regexp.MustCompile(`\{([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}),"([^"]*)"[^"]*\{"([^"]*)",(\d+)\}`)

// Cluster — кластер из текстового ответа.
type Cluster struct {
	Uuid, Name string
	Host       string
	Port       int
}

// Clusters — список кластеров (кадр-запрос tmplClusters).
func (c *Client) Clusters() ([]Cluster, error) {
	if err := c.send(c.fClusters); err != nil {
		return nil, err
	}
	resp, err := c.readFrame()
	if err != nil && len(resp) == 0 {
		return nil, err
	}
	var out []Cluster
	for _, m := range clusterRe.FindAllStringSubmatch(string(resp), -1) {
		var p int
		fmt.Sscanf(m[4], "%d", &p)
		out = append(out, Cluster{Uuid: m[1], Name: m[2], Host: m[3], Port: p})
	}
	return out, nil
}

// strings9a — все 9a-строки из бинарного ответа (utf-8, с длиной-байтом).
func strings9a(d []byte) []string {
	var out []string
	i := 0
	for i < len(d)-2 {
		if d[i] == 0x9a {
			ln := int(d[i+1])
			if ln > 0 && i+2+ln <= len(d) && utf8.Valid(d[i+2:i+2+ln]) {
				out = append(out, string(d[i+2:i+2+ln]))
				i += 2 + ln
				continue
			}
		}
		i++
	}
	return out
}

// Base — имя + настоящий uuid базы.
type Base struct {
	Name, Uuid string
}

// Bases — информационные базы (кадр tmplBases, цепочка до него обязательна).
// Список устроен со сдвигом: [uuid0] имя0 [81 f5 uuid1] имя1 [81 f5 uuid2]… —
// f5-uuid после имени N принадлежит СЛЕДУЮЩЕЙ базе (проверено живым запросом:
// uuid после conversion3 принёс сеансы 159).
func (c *Client) Bases() ([]Base, error) {
	if _, err := c.req(c.fDiag); err != nil { // служебные для словаря
		return nil, fmt.Errorf("диагностика: %w", err)
	}
	if _, err := c.req(c.fCh95); err != nil {
		return nil, fmt.Errorf("канал: %w", err)
	}
	resp, err := c.req(c.fBases)
	if err != nil {
		return nil, fmt.Errorf("базы: %w", err)
	}
	type item struct {
		uuidBefore string // uuid95 перед именем
		name       string
		f5After    string // f5-uuid сразу после имени
	}
	var items []item
	i, pend := 0, ""
	for i < len(resp)-2 {
		switch {
		case resp[i] == 0x9a && resp[i+1] > 0 && i+2+int(resp[i+1]) <= len(resp):
			if utf8.Valid(resp[i+2 : i+2+int(resp[i+1])]) {
				items = append(items, item{uuidBefore: pend, name: string(resp[i+2 : i+2+int(resp[i+1])])})
				pend = ""
				i += 2 + int(resp[i+1])
				continue
			}
			i += 2 + int(resp[i+1])
		case resp[i] == 0x95 && i+17 <= len(resp):
			pend = guidFromLE(resp[i+1 : i+17])
			i += 17
		case resp[i] == 0xf5 && i+17 <= len(resp) && len(items) > 0:
			items[len(items)-1].f5After = string(resp[i+1 : i+17])
			i += 17
		default:
			i++
		}
	}
	var out []Base
	for k, it := range items {
		if it.name == "" {
			continue
		}
		u := it.uuidBefore
		if u == "" && k > 0 {
			u = guidFromLE([]byte(items[k-1].f5After)) // uuid предыдущей записи
		}
		out = append(out, Base{Name: it.name, Uuid: u})
	}
	return out, nil
}

// Sessions — сеансы информационных баз: по каждой базе отдельный 128-кадр
// с её uuid (проверено: uuid после conv3 → сеансы 159). Счётчик запроса
// инкрементируется (off 0x13, uint16 LE).
func (c *Client) Sessions(bases []Base) ([]Session, error) {
	var out []Session
	q := append([]byte(nil), c.fConn128...)
	base := counterLE(q)
	for _, b := range bases {
		bq := append([]byte(nil), q...)
		setCounterLE(bq, base)
		if ub, err := uuidLEBytes(b.Uuid); err == nil {
			copy(bq[0x69:0x79], ub)
		} else {
			continue // без uuid базы сеансы не спросить
		}
		r, err := c.req(bq)
		if err != nil {
			return out, fmt.Errorf("сеансы %s: %w", b.Name, err)
		}
		for _, s := range parseSessions(r) {
			if s.Uuid == "" {
				// сгенерить стабильный, чтобы TUI не путал записи
				s.Uuid = b.Uuid + "/" + s.User + "/" + s.Host + "/" + s.App
			}
			out = append(out, s)
		}
		base++
	}
	return out, nil
}

func counterLE(q []byte) uint16 { return binary.LittleEndian.Uint16(q[0x13:0x15]) }

func setCounterLE(q []byte, v uint16) { binary.LittleEndian.PutUint16(q[0x13:0x15], v) }

// uuidLEBytes — канонический uuid → 16 байт GUID-LE.
func uuidLEBytes(u string) ([]byte, error) {
	hx := strings.ReplaceAll(u, "-", "")
	if len(hx) != 32 {
		return nil, fmt.Errorf("кривой uuid %q", u)
	}
	b := make([]byte, 16)
	for i := 0; i < 16; i++ {
		var v byte
		if _, err := fmt.Sscanf(hx[i*2:i*2+2], "%02x", &v); err != nil {
			return nil, err
		}
		b[i] = v
	}
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
	b[4], b[5] = b[5], b[4]
	b[6], b[7] = b[7], b[6]
	return b, nil
}



// parseSessions — маркер записи: 9a-строка, за которой две даты (91 91);
// далее строки хост/юзер/приложение. Перед именем — до трёх uuid:
// сеанс, юзер (у фоновых отсутствует), база.
func parseSessions(d []byte) []Session {
	type tok struct {
		kind string // "str", "uuid", "date"
		val  string
		num  int64
		raw  []byte
	}
	var toks []tok
	i := 0
	for i < len(d)-2 {
		t := d[i]
		switch {
		case t == 0x9a && d[i+1] > 0 && i+2+int(d[i+1]) <= len(d):
			if utf8.Valid(d[i+2 : i+2+int(d[i+1])]) {
				toks = append(toks, tok{"str", string(d[i+2 : i+2+int(d[i+1])]), 0, nil})
				i += 2 + int(d[i+1])
				continue
			}
		case (t == 0x95 || t == 0xd5) && i+17 <= len(d):
			toks = append(toks, tok{"uuid", "", 0, d[i+1 : i+17]})
			i += 17
			continue
		case t == 0x91 && i+9 <= len(d):
			toks = append(toks, tok{"date", "", int64(binary.LittleEndian.Uint64(d[i+1 : i+9])), nil})
			i += 9
			continue
		}
		i++
	}
	var out []Session
	for k := 0; k+5 < len(toks); k++ {
		if toks[k].kind != "str" || toks[k+1].kind != "date" || toks[k+2].kind != "date" {
			continue
		}
		// строки после дат: хост, юзер, затем поля записи (для пользовательских
		// сеансов 3-я строка = приложение; у консольных: server1c, «182»,
		// SrvrConsole — приложение не на 3-й позиции)
		var ss []string
		for j := k + 3; j < len(toks) && j <= k+8 && toks[j].kind == "str"; j++ {
			ss = append(ss, toks[j].val)
		}
		if len(ss) < 3 {
			continue
		}
		// приложение ищем по всем строкам после хоста: у сеанса с пустым
		// юзером (экран логина) строка приложения идёт сразу за хостом
		app := ""
		kApp := -1
		for j, cand := range ss[1:] {
			if knownApp(cand) != "" {
				app = cand
				kApp = j + 1
				break
			}
		}
		if app == "" {
			continue // без известного приложения — мусор парсера
		}
		user := ""
		if kApp > 1 {
			user = ss[1] // первая строка после хоста (лишние поля записи не клеим)
		}
		// даты живого сеанса: 2020–2035 (тики 100мкс с 0001)
		if toks[k+1].num < 637000000000000 || toks[k+1].num > 660000000000000 {
			continue
		}
		s := Session{InfobaseName: toks[k].val, Started: toks[k+1].num, LastActive: toks[k+2].num,
			Host: ss[0], User: user, App: app}
		// uuid перед именем, ближайшие 2-3. РОЛИ (проверено живым килем
		// против эталонного захвата консоли 16.09): ПОСЛЕДНИЙ uuid перед
		// именем — база, ПРЕДПОСЛЕДНИЙ — сеанс (у записей из двух uuid это
		// ids[0], из трёх — ids[1]; первый — служебный, вероятно подключение).
		// Прежнее «ids[0] = сеанс» путало роли в трёхuuid-записях: кил
		// уходил по чужому uuid — сервер ackал, сеанс жил.
		var ids [][]byte
		for j := k - 1; j >= 0 && len(ids) < 3; j-- {
			if toks[j].kind != "uuid" {
				break
			}
			ids = append([][]byte{toks[j].raw}, ids...)
		}
		if len(ids) >= 2 {
			s.Uuid = guidFromLE(ids[len(ids)-2])
			s.InfobaseUUID = guidFromLE(ids[len(ids)-1])
			if len(ids) == 3 {
				s.UserUUID = guidFromLE(ids[0])
			}
		} else if len(ids) == 1 {
			s.Uuid = guidFromLE(ids[0])
		}
		out = append(out, s)
	}
	return out
}

// knownApp — известные app-id 1С; пусто = строка не приложение (мусор парсера).
func knownApp(s string) string {
	switch s {
	case "1CV8C", "1CV8", "Designer", "BackgroundJob", "JobScheduler",
		"SrvrConsole", "RAS", "COMConnector", "AgentStandardCall", "WebClient":
		return s
	}
	return ""
}

// guidFromLE — 16 байт GUID-LE → каноничный uuid.
func guidFromLE(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	le := make([]byte, 16)
	copy(le, b)
	le[0], le[1], le[2], le[3] = b[3], b[2], b[1], b[0]
	le[4], le[5] = b[5], b[4]
	le[6], le[7] = b[7], b[6]
	return fmt.Sprintf("%x-%x-%x-%x-%x", le[0:4], le[4:6], le[6:8], le[8:10], le[10:16])
}

// AllSessions — полный список сеансов кластера одним кадром (112).
func (c *Client) AllSessions() ([]Session, error) {
	r, err := c.req(c.fAllSessions)
	if err != nil {
		return nil, fmt.Errorf("все сеансы: %w", err)
	}
	return parseSessions(r), nil
}

// Terminate — завершение сеанса: побайтный клон реплейного кила консоли
// (снимок research/spy83-frames/1353…, 16.09 — совпадает с tmplTerminate
// побайтно) с заменой ровно ДВУХ uuid: база @0x69 и жертва @0x90 (по
// 95-тегам). Слот @0x7e — uuid ОПЕРАТОРА консоли из реплея — не трогать:
// вписывание туда uuid пользователя жертвы ломает кил (сервер ackает 18Б,
// но сеанс жив — проверено живьём). Сообщение и счётчик — реплейные:
// собственная пересборка тоже ломала кадр. Ответ 18Б ack, 4Б — отказ.
func (c *Client) Terminate(baseUuid, sessionUuid string) error {
	q := append([]byte(nil), tmplTerminate...)
	bu, err := uuidLEBytes(baseUuid)
	if err != nil {
		return err
	}
	su, err := uuidLEBytes(sessionUuid)
	if err != nil {
		return err
	}
	copy(q[0x69:0x79], bu)
	copy(q[0x90:0xa0], su)
	if os.Getenv("RA83DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "RA83 terminate-запрос %dБ: % x\n", len(q), q)
	}
	r, err := c.req(q)
	if err != nil {
		return err
	}
	if os.Getenv("RA83DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "RA83 terminate-ответ %dБ: % x\n", len(r), r)
	}
	if len(r) < 10 {
		return fmt.Errorf("terminate: короткий ответ %dБ", len(r))
	}
	return nil
}

// Props — ответ свойств базы: [9a имя][a1 cb][uuid16][хвост] в 141-кадре.
// Ответ: 9a "host:port" a4 81 81 81 81 a3 — четыре флага (по гипотезе
// среди них запрет сеансов и запрет РЗ; проверяется живым toggle-тестом).
func (c *Client) Props(name, uuid string) ([]byte, error) {
	ub, err := uuidLEBytes(uuid)
	if err != nil {
		return nil, err
	}
	// tmplProps: [9a 08 "erp_demo"][a1 cb][uuid16 erp_demo][хвост]
	head := c.fProps[:0x56]                                 // до 9a
	oldName := c.fProps[0x58 : 0x58+int(c.fProps[0x57])]   // "erp_demo"
	nb := append([]byte(nil), head...)
	nb = append(nb, 0x9a, byte(len(name)))
	nb = append(nb, name...)
	nb = append(nb, c.fProps[0x58+len(oldName):0x76]...)   // a1 cb + разделители
	nb = append(nb, ub...)
	nb = append(nb, c.fProps[0x86:]...)                    // хвост
	setCounterLE(nb, counterLE(c.fProps))
	r, err := c.req(nb)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ToggleSessionsDeny — переключение запрета начала сеансов (toggle-кадр!).
// Состояние НЕ передаётся: сервер переключает текущее. Только чтение
// свойств знает состояние. Тело реплея несёт контекст базы словарём.
func (c *Client) ToggleSessionsDeny() error {
	r, err := c.req(tmplToggle)
	if err != nil {
		return err
	}
	if len(r) == 0 {
		return fmt.Errorf("toggle: пустой ответ")
	}
	return nil
}
