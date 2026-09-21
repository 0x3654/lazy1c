// Package ras82 — прототип нативного клиента ragent 1С:Предприятие 8.2 (MMC-протокол, порт 1540).
// Протокол: приветствие → крипто-конверт+интро → NTLM-anon → запрос сессии →
// бинарные кадры данных. Управляющие сообщения — 1С-текстовый формат, данные —
// бинарная сериализация 1С (пока разбирается эвристически).
// Шаблоны сообщений — реплей из захвата research/spy3.log (см. templates.go).
package ras82

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

// приветы и трейлеры протокола
var (
	greetBytes = []byte{0x53, 0xf5, 0xc6, 0x1a, 0x7b} // ragent шлёт первым
	frameTail  = []byte{0x66, 0x53, 0xb2, 0xa6}       // завершитель каждого кадра
)

// Value — эвристически извлечённое значение из бинарной сериализации.
type Value struct {
	Kind string // "str", "str16", "uuid", "time"
	Text string
	T    time.Time // для kind == "time"
	Tag  byte      // исходный тег байта (95=uuid юзера, d5=uuid сеанса, наблюдение из захватов)
}

// Client — одно соединение с ragent 8.2.
type Client struct {
	version      string    // версия платформы, выученная из ответов
	sawAuthErr   bool      // ответ содержал «…не зарегистрирован»
	terminate    []byte    // terminate-кадр выбранного набора
	usedSet      *frameSet // набор последнего успешного FetchAll
	lastClusters []Value   // кластеры последнего полного FetchAll
	conn         net.Conn
	sess         string // uuid сессии из auth-ответа
	sessLE       []byte // та же uuid в GUID-LE (для бинарных кадров)
	counter      uint16 // счётчик бинарных кадров (8f <LE16>), старт — как в захвате
	timeout      time.Duration
}

// Session82 — запись сеанса, собранная из значений бинарного ответа.
type Session82 struct {
	ID, UserUUID        string // сеансовый uuid (стабилен) и uuid пользователя
	InfobaseName        string
	Started, LastActive time.Time
	Host, User, App     string
	Locale, Ver         string
	idCands             []Value // uuid-кандидаты перед именем базы — разрешаются пост-пасом
}

// Dial подключается (анонимный NTLM — для кластеров без доменной аутентификации).
func Dial(addr string, timeout time.Duration) (*Client, error) {
	return DialAuth(addr, timeout, false)
}

// DialAuth подключается с выбранным вариантом NTLM3: domain=true — доменная
// подпись (кластеры с доменной аутентификацией), false — анонимная.
func DialAuth(addr string, timeout time.Duration, domain bool) (*Client, error) {
	ntlm3 := tmplNtlm3_522
	if domain {
		ntlm3 = tmplNtlm3Domain
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, timeout: timeout}
	greet := make([]byte, len(greetBytes))
	if err := c.readFull(greet); err != nil {
		conn.Close()
		return nil, fmt.Errorf("приветствие: %w", err)
	}
	if string(greet) != string(greetBytes) {
		conn.Close()
		return nil, fmt.Errorf("это не ragent 8.2 (приветствие %x) — похоже на RAS 8.3+?", greet)
	}
	if err := c.handshake(ntlm3); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// DialAuto подключается анонимно, прогоняет упорядоченный опрос и подбирает
// набор кадров: сначала закэшированный/прод, при пустом результате — VM.
// Возвращает клиент (соединение открыто) и лучший список.
func DialAuto(addr string, timeout time.Duration) (*Client, *Lists, error) {
	sets := []*frameSet{&prodFrames, &vmFrames}
	var bestCl *Client
	var bestLists *Lists
	for _, fs := range sets {
		cl, err := Dial(addr, timeout)
		if err != nil {
			if bestCl != nil {
				bestCl.Close()
			}
			return nil, nil, err
		}
		l, err := cl.FetchAll(fs)
		if err == nil && l.score() > 0 {
			if bestCl != nil {
				bestCl.Close()
			}
			return cl, l, nil // живой набор — договорились
		}
		if err == nil && bestLists == nil { // пусто, но не сломалось — запоминаем как запасной
			bestCl, bestLists = cl, l
			continue
		}
		cl.Close()
	}
	if bestLists != nil {
		return bestCl, bestLists, nil // сервер просто пуст (как VM без баз)
	}
	return nil, nil, fmt.Errorf("ни один набор кадров не принят ragent")
}

// Close разрывает соединение.
func (c *Client) Close() { c.conn.Close() }

// Sess возвращает uuid открытой сессии.
func (c *Client) Sess() string { return c.sess }

// handshake: конверт → конверт-ответ; NTLM1 → challenge; NTLM3 → auth;
// открытие сессии {0,<uuid>,seq,1,<reqid>}.
func (c *Client) handshake(ntlm3 []byte) error {
	if _, err := c.send(tmplEnv654); err != nil {
		return fmt.Errorf("конверт: %w", err)
	}
	if _, err := c.readFrame(); err != nil { // конверт-ответ (заголовок + бинарный хвост)
		return fmt.Errorf("конверт-ответ: %w", err)
	}
	if _, err := c.send(tmplNtlm1_474); err != nil {
		return fmt.Errorf("ntlm1: %w", err)
	}
	if _, err := c.readFrame(); err != nil { // NTLM challenge
		return fmt.Errorf("challenge: %w", err)
	}
	if _, err := c.send(ntlm3); err != nil {
		return fmt.Errorf("ntlm3: %w", err)
	}
	auth, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("auth-ответ: %w", err)
	}
	m := regexp.MustCompile(`\{1,\d+,1,\r\n\{"#",[0-9a-f-]{36},\r\n\{([0-9a-f-]{36})\}`).FindSubmatch(auth)
	if m == nil {
		return fmt.Errorf("auth-ответ без uuid сессии: %.120s", auth)
	}
	c.sess = string(m[1])
	c.sessLE = guidLE(c.sess)
	c.counter = 0x4ed4 // стартовое значение из захвата; +1 на каждый бинарный кадр
	if v := introVersionRe.Find(tmplEnv654); v != nil {
		c.version = string(v[1 : len(v)-1]) // без кавычек; ragent принял интро с этой версией — несовместимую отверг бы
	}

	req := []byte(string(tmplSessReq97[:6]) + c.sess + string(tmplSessReq97[42:]))
	if _, err := c.send(req); err != nil {
		return fmt.Errorf("запрос сессии: %w", err)
	}
	if _, err := c.readFrame(); err != nil { // {1,<id>,39}
		return fmt.Errorf("ответ на сессию: %w", err)
	}
	return nil
}

// frame — один кадр последовательности и его роль ("" = служебный для словаря).
type frame struct {
	tmpl []byte
	role string // "clusters", "infobases", "sessions"
}

// frameSet — упорядоченная цепочка кадров запросов. Кадры содержат ссылки
// в потоковый словарь соединения: пропуск/перестановка ломает ссылки,
// поэтому набор — это последовательность, снятая с живой MMC целиком.
type frameSet struct {
	seq       []frame
	terminate []byte // кадр завершения сеанса: uuid жертвы в [107:123]
}

// vmFrames — набор с VM-стенда (research/spy3.log).
// NB: старый tmplBinInfobases (99Б) — не базы; настоящий запрос баз — tmplBinConn.
var vmFrames = frameSet{seq: []frame{
	{tmplBinClusters, "clusters"},
	{tmplBinRights, ""},
	{tmplBinInfobases, ""},
	{tmplBinConn, "infobases"},
	{tmplBinSessions, "sessions"},
},
	terminate: tmplBinTerminate,
}

// Lists — результаты полного опроса (все кадры в порядке захвата MMC).
type Lists struct {
	Clusters    []Value
	Infobases   []string
	InfobaseIDs map[string]string // имя → настоящий uuid базы (из списка)
	Sessions    []Session82
}

// FetchAll прогоняет цепочку кадров набора в исходном порядке; ответы
// накапливаются по ролям. Порядок критичен: кадры ссылаются в потоковый словарь.
func (c *Client) FetchAll(fs *frameSet) (*Lists, error) {
	l := &Lists{InfobaseIDs: map[string]string{}}
	c.terminate = fs.terminate
	c.usedSet = fs
	for _, f := range fs.seq {
		vals, err := c.binRequest(f.tmpl)
		if err != nil {
			return nil, err
		}
		switch f.role {
		case "clusters":
			l.Clusters = vals
			c.lastClusters = vals
		case "infobases":
			// как в FetchQuick: uuid базы идёт после имени — собираем, чтобы
			// uuid баз не менялись между полным и быстрым опросами
			var pendName string
			for _, v := range vals {
				switch v.Kind {
				case "str", "str16":
					if strings.Contains(v.Text, "Ошибка") || strings.Contains(v.Text, "не зарегистрирован") {
						c.sawAuthErr = true
						continue
					}
					l.Infobases = append(l.Infobases, v.Text)
					pendName = v.Text
				case "uuid":
					if pendName != "" && l.InfobaseIDs[pendName] == "" {
						l.InfobaseIDs[pendName] = v.Text
						pendName = ""
					}
				}
			}
		case "sessions":
			l.Sessions = assembleSessions(vals)
			resolveSessionIDs(l.Sessions)
		}
	}
	return l, nil
}

// resolveSessionIDs раскладывает uuid-кандидаты записей: uuid пользователя
// повторяется у сеансов одного юзера и чаще всего с тегом 0x95, сеансовый —
// уникален и наблюдается с тегом 0xd5. Порядок в записи нестабилен.
func resolveSessionIDs(ss []Session82) {
	freq := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, c := range s.idCands {
			if !seen[c.Text] {
				freq[c.Text]++
				seen[c.Text] = true
			}
		}
	}
	for i := range ss {
		s := &ss[i]
		if len(s.idCands) == 0 {
			continue
		}
		var user, sess *Value
		for j := range s.idCands {
			c := &s.idCands[j]
			if freq[c.Text] > 1 || c.Tag == 0x95 { // повторяется или тег юзера
				if user == nil {
					user = c
				}
				continue
			}
			if sess == nil || c.Tag == 0xd5 { // предпочтение d5-тегу сеанса
				sess = c
			}
		}
		if sess == nil && user != nil && len(s.idCands) > 1 { // все повторяются — берём не-95
			for j := range s.idCands {
				if &s.idCands[j] != user && s.idCands[j].Tag != 0x95 {
					sess = &s.idCands[j]
					break
				}
			}
		}
		if sess == nil && len(s.idCands) > 0 && s.idCands[len(s.idCands)-1].Tag != 0x95 {
			sess = &s.idCands[len(s.idCands)-1] // запасной: последний не-95
		}
		if user != nil {
			s.UserUUID = user.Text
		}
		if sess != nil {
			s.ID = sess.Text
		}
		if s.ID == "" { // совсем ничего — деградируем до прежнего поведения
			s.ID = s.UserUUID
		}
	}
}

// FetchQuick — быстрый опрос живого соединения: только базы и сеансы
// (словарь уже построен, 2 кадра вместо полной цепочки — ~10× быстрее).
// Кластеры берутся из последнего полного FetchAll.
func (c *Client) FetchQuick(fs *frameSet, clusters []Value) (*Lists, error) {
	l := &Lists{Clusters: clusters, InfobaseIDs: map[string]string{}}
	for _, f := range fs.seq {
		if f.role != "infobases" && f.role != "sessions" {
			continue
		}
		vals, err := c.binRequest(f.tmpl)
		if err != nil {
			return nil, err
		}
		if f.role == "sessions" {
			l.Sessions = assembleSessions(vals)
			resolveSessionIDs(l.Sessions)
		} else {
			// запись базы: [9a имя][81 флаг][f5 uuid базы] — uuid идёт ПОСЛЕ имени
			var pendName string
			for _, v := range vals {
				switch v.Kind {
				case "str", "str16":
					if strings.Contains(v.Text, "Ошибка") || strings.Contains(v.Text, "не зарегистрирован") {
						c.sawAuthErr = true
						continue
					}
					l.Infobases = append(l.Infobases, v.Text)
					pendName = v.Text
				case "uuid":
					if pendName != "" && l.InfobaseIDs[pendName] == "" {
						l.InfobaseIDs[pendName] = v.Text // uuid базы после имени
						pendName = ""
					}
				}
			}
		}
	}
	return l, nil
}

// LastClusters — кластеры последнего полного опроса (для FetchQuick).
func (c *Client) LastClusters() []Value { return c.lastClusters }

// score — сколько полезных данных принес опрос (для выбора набора кадров).
func (l *Lists) score() int { return len(l.Infobases) + len(l.Sessions) }

// Clusters запрашивает список кластеров (VM-набор; внутри FetchAll используется набор соединения).
func (c *Client) Clusters() ([]Value, error) { return c.binRequest(vmFrames.seq[0].tmpl) }

// Sessions запрашивает список сеансов и собирает их в записи.
func (c *Client) Sessions() ([]Session82, error) {
	vals, err := c.binRequest(vmFrames.seq[4].tmpl)
	if err != nil {
		return nil, err
	}
	return assembleSessions(vals), nil
}

// Infobases запрашивает список информационных баз (имена).
// Ошибки сервер прилетают строками («Ошибка…», «…не зарегистрирован») — отфильтровываем.
func (c *Client) Infobases() ([]string, error) {
	vals, err := c.binRequest(vmFrames.seq[3].tmpl)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, v := range vals {
		if v.Kind != "str" && v.Kind != "str16" {
			continue
		}
		if strings.Contains(v.Text, "Ошибка") || strings.Contains(v.Text, "не зарегистрирован") {
			c.sawAuthErr = true
			continue
		}
		names = append(names, v.Text)
	}
	return names, nil
}

// NeedsDomain — кластер отверг анонимный NTLM («не зарегистрирован»):
// повторить соединение с доменной подписью.
func (c *Client) NeedsDomain() bool { return c.sawAuthErr }

// Terminate завершает сеанс кадром активного набора (после FetchAll).
func (c *Client) Terminate(sessionID, userUUID string) error {
	tmpl := c.terminate
	if tmpl == nil {
		tmpl = tmplBinTerminate
	}
	return c.TerminateWith(tmpl, sessionID, userUUID)
}

// TerminateWith шлёт terminate-кадр: [107:123] = uuid пользователя,
// [146:162] = сеансовый uuid (оба из записи сеанса). Ответ 0x42 = принят.
func (c *Client) TerminateWith(tmpl []byte, sessionID, userUUID string) error {
	frame := make([]byte, len(tmpl))
	copy(frame, tmpl)
	copy(frame[2:18], c.sessLE)
	if userUUID != "" {
		copy(frame[107:123], guidLE(userUUID))
	}
	copy(frame[146:162], guidLE(sessionID))
	c.counter++
	binary.LittleEndian.PutUint16(frame[19:21], c.counter)
	if _, err := c.send(frame); err != nil {
		return err
	}
	resp, err := c.readReply()
	if err != nil {
		return err
	}
	if len(resp) == 0 || resp[0] != 0x42 {
		return fmt.Errorf("ragent отверг кадр: %x", resp[:min(len(resp), 16)])
	}
	if os.Getenv("RA82DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "RA82 terminate-ответ %dБ: %x\n", len(resp), resp)
	}
	if len(resp) < 13 { // 11Б = «сеанс не найден», 17Б = выполнено
		return fmt.Errorf("сеанс не найден ragent (ответ %x)", resp)
	}
	return nil
}

// binRequestRaw — как binRequest, но возвращает сырые байты ответа (для диффов).
func (c *Client) binRequestRaw(tmpl []byte) ([]byte, error) {
	frame := make([]byte, len(tmpl))
	copy(frame, tmpl)
	copy(frame[2:18], c.sessLE)
	c.counter++
	binary.LittleEndian.PutUint16(frame[19:21], c.counter)
	if _, err := c.send(frame); err != nil {
		return nil, err
	}
	return c.readReply()
}

// binRequest подставляет GUID-LE сессии [2:18] и счётчик [19:21], шлёт и разбирает ответ.
func (c *Client) binRequest(tmpl []byte) ([]Value, error) {
	frame := make([]byte, len(tmpl))
	copy(frame, tmpl)
	copy(frame[2:18], c.sessLE)
	c.counter++
	binary.LittleEndian.PutUint16(frame[19:21], c.counter)
	if _, err := c.send(frame); err != nil {
		return nil, err
	}
	resp, err := c.readFrame()
	if os.Getenv("RA82DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "RA82 >> %dБ ответ: %x\n", len(resp), resp[:min(len(resp), 80)])
	}
	if err != nil {
		return nil, err
	}
	if m := versionRe.FindSubmatch(resp); m != nil && c.version == "" {
		c.version = string(m[1]) // версия платформы живёт в ответах (сеансы и др.)
	}
	return ParseBin(resp), nil
}

// introVersionRe — версия из нашего интро (сервер её принял = совместим).
var introVersionRe = regexp.MustCompile(`"8\.[0-9]+\.[0-9]+\.[0-9]+"`)

// versionRe выхватывает версию платформы из бинарных ответов (9a <len> "8.2.19.130").
var versionRe = regexp.MustCompile(`\x9a[0-9a-f]{0,2}(8\.2\.[0-9]+\.[0-9]+)`)

// Version — версия платформы из ответов ("" если пока не встречалась).
func (c *Client) Version() string { return c.version }

// assembleSessions склеивает записи сеансов: якорь — пара время/время,
// перед ней uuid сеанса и имя базы, после — хост, приложение, язык, версия.
func assembleSessions(vals []Value) []Session82 {
	var out []Session82
	for i := 0; i < len(vals); i++ {
		if vals[i].Kind != "time" || i+1 >= len(vals) || vals[i+1].Kind != "time" {
			continue
		}
		s := Session82{Started: vals[i].T, LastActive: vals[i+1].T}
		// влево от пары времён: [строка-база][серия uuid][…хвост прошлой записи];
		// собираем сплошную серию uuid сразу левее базы — порядок в ней нестабилен
		// (словарное сжатие), роли (сеанс/юзер) разрешаем пост-пасом по тегам
		var cands []Value
		phase := 0 // 0 = ищем базу, 1 = собираем uuid-серию
		for j := i - 1; j >= 0 && j >= i-9; j-- {
			isStr := vals[j].Kind == "str" || vals[j].Kind == "str16"
			switch {
			case phase == 0 && isStr:
				s.InfobaseName = vals[j].Text
				phase = 1
			case phase == 1 && vals[j].Kind == "uuid":
				cands = append(cands, vals[j])
			case phase == 1:
				if len(cands) > 0 || !isStr && vals[j].Kind != "time" {
					// серия кончилась (или начался хвост прошлой записи) — стоп
				}
				if len(cands) > 0 {
					continue // хвост: числа/мусор — пропускаем, но uuid-серию уже не ищем
				}
			}
			if phase == 1 && len(cands) >= 3 {
				break
			}
		}
		s.idCands = cands
		// вперёд: хост, приложение, язык, язык, затем uuid и версия
		var strs []string
		for j := i + 2; j < len(vals) && j <= i+8; j++ {
			if vals[j].Kind == "str" || vals[j].Kind == "str16" {
				strs = append(strs, vals[j].Text)
			}
		}
		if len(strs) > 0 {
			s.Host = strs[0]
		}
		// порядок полей после хоста зависит от сервера: VM — [прил, язык, язык],
		// прод — [юзер, [сервер], …, язык]; приложение ищем по известным именам
		knownApps := map[string]bool{"SrvrConsole": true, "Designer": true, "1CV8": true,
			"1CV8C": true, "WebClient": true, "JobScheduler": true, "COMConsole": true}
		for _, v := range strs[1:] {
			switch {
			case knownApps[v]:
				s.App = v
			case strings.HasPrefix(v, "8."):
				s.Ver = v
			case len(v) == 5 && v[2] == '_':
				s.Locale = v
			case strings.HasPrefix(v, "[["):
				if s.App == "" {
					s.App = v // «[[server-agent]]» — серверное приложение 8.2
				}
			default:
				if s.User == "" {
					s.User = v // ФИО пользователя
				}
			}
		}
		if s.App == "" && len(strs) > 1 {
			s.App = strs[1]
		}
		out = append(out, s)
		i++ // пропустить второе время пары
	}
	return out
}

// --- транспорт ---

func (c *Client) send(b []byte) (int, error) {
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.conn.Write(b)
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

// readReply читает ответ на запрос (первый кадр), затем вычитывает
// накопившиеся асинхронные пуши — иначе следующий запрос получит чужой кадр.
func (c *Client) readReply() ([]byte, error) {
	f, err := c.readFrame()
	if err == nil {
		c.drainPushes()
	}
	return f, err
}

// ReadRawFrame читает один кадр с произвольным таймаутом (для отладки).
func (c *Client) ReadRawFrame(d time.Duration) ([]byte, error) {
	save := c.timeout
	c.timeout = d
	f, err := c.readFrame()
	c.timeout = save
	return f, err
}

// drainPushes вычитывает кадры, уже стоящие в сокете (150мс окно).
func (c *Client) drainPushes() {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		buf := make([]byte, 8192)
		n, err := c.conn.Read(buf)
		if n == 0 || err != nil {
			return // тишина — пушей нет
		}
	}
}

// readFrame читает кадр до трейлера 66 53 B2 A6 в конце потока.
func (c *Client) readFrame() ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 8192)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(c.timeout))
		n, err := c.conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if idx := lastIndex(buf, frameTail); idx >= 0 && idx+len(frameTail) == len(buf) {
				return buf, nil // трейлер в конце — кадр полный
			}
		}
		if err != nil {
			if len(buf) > 4 && strings.Contains(err.Error(), "i/o timeout") {
				return buf, nil // тихий таймаут после данных — кадр закончился
			}
			return buf, err
		}
		if len(buf) > 1<<20 {
			return buf, fmt.Errorf("кадр слишком большой")
		}
	}
}

func lastIndex(b, sub []byte) int {
	for i := len(b) - len(sub); i >= 0; i-- {
		match := true
		for j := range sub {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// --- разбор бинарной сериализации (эвристика) ---

// ParseBin извлекает значения: utf8-строки 0x9a<len>, UTF-16LE-фрагменты,
// uuid 0x95<16Б>, время 0x91<8Б FILETIME>.
func ParseBin(b []byte) []Value {
	var out []Value
	i := 0
	for i < len(b) {
		switch {
		case b[i] == 0x9a && i+1 < len(b):
			n := int(b[i+1])
			if i+2+n <= len(b) && printableASCII(b[i+2:i+2+n]) {
				out = append(out, Value{Kind: "str", Text: string(b[i+2 : i+2+n])})
				i += 2 + n
				continue
			}
		case b[i] == 0x81 || b[i] == 0x82: // булевы: 81=false, 82=true
			v := "false"
			if b[i] == 0x82 {
				v = "true"
			}
			out = append(out, Value{Kind: "bool", Text: v, Tag: b[i]})
			i++
			continue
		case b[i]&0x0f == 0x05 && i+16 < len(b) && looksUUID(b[i+1:i+17]):
			out = append(out, Value{Kind: "uuid", Text: leGUID(b[i+1 : i+17]), Tag: b[i]})
			i += 17
			continue
		case b[i] == 0x91 && i+8 < len(b): // дата 1С
			ft := binary.LittleEndian.Uint64(b[i+1 : i+9])
			t := fileTime(ft)
			if y := t.Year(); y >= 2000 && y <= 2100 { // фильтр ложных 0x91 внутри потока
				out = append(out, Value{Kind: "time", Text: t.Format("02.01.2006 15:04:05"), T: t})
				i += 9
				continue
			}
		}
		if s, n := utf16Run(b[i:]); n >= 8 {
			out = append(out, Value{Kind: "str16", Text: s})
			i += n
			continue
		}
		i++
	}
	return out
}

// looksUUID — сырые 16 LE-байт похожи на настоящий uuid. Вариант обязан быть
// 8-b (мусор перед строками его не проходит); версия у 1С бывает нестандартной
// (базы — 0xF), поэтому вместо версии отсекаем байтовый мусор (≥5 одинаковых).
func looksUUID(b []byte) bool {
	if variant := b[8] >> 4; variant < 8 || variant > 0xb {
		return false
	}
	cnt := [256]int{}
	for _, x := range b {
		cnt[x]++
		if cnt[x] >= 5 {
			return false
		}
	}
	return true
}

func printableASCII(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// utf16Run ищет в начале фрагмент UTF-16LE (латиница, кириллица, пробел).
func utf16Run(b []byte) (string, int) {
	var sb strings.Builder
	n := 0
	for n+1 < len(b) {
		r := rune(b[n]) | rune(b[n+1])<<8
		if (r >= 0x20 && r <= 0x7e) || (r >= 0x400 && r <= 0x4ff) || r == 0x2013 {
			sb.WriteRune(r)
			n += 2
		} else {
			break
		}
	}
	return sb.String(), n
}

// guidLE превращает строку uuid в 16 байт GUID-LE (data1..3 little-endian).
func guidLE(s string) []byte {
	hexs := strings.ReplaceAll(s, "-", "")
	all := make([]byte, 16)
	for i := 0; i < 16; i++ {
		var v byte
		fmt.Sscanf(hexs[i*2:i*2+2], "%02x", &v)
		all[i] = v
	}
	out := make([]byte, 16)
	out[0], out[1], out[2], out[3] = all[3], all[2], all[1], all[0]
	out[4], out[5] = all[5], all[4]
	out[6], out[7] = all[7], all[6]
	copy(out[8:], all[8:])
	return out
}

// leGUID читает GUID-LE обратно в строку.
func leGUID(b []byte) string {
	all := make([]byte, 16)
	all[0], all[1], all[2], all[3] = b[3], b[2], b[1], b[0]
	all[4], all[5] = b[5], b[4]
	all[6], all[7] = b[7], b[6]
	copy(all[8:], b[8:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", all[0:4], all[4:6], all[6:8], all[8:10], all[10:16])
}

// fileTime конвертирует дату 1С: LE int64, 100-мкс тики с 0001-01-01.
// Считается через Unix-эпоху: Duration не вмещает наносекунды с года 1.
func fileTime(ft uint64) time.Time {
	secs := int64(ft/10000) - 62135596800 // сек с 1970 (эпоха 1С минус Unix)
	nsec := int64(ft%10000) * 100000
	return time.Unix(secs, nsec).UTC()
}
