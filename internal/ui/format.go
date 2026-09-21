package ui

import (
	"fmt"
	"strings"
	"time"

	serializev1 "github.com/v8platform/protos/gen/v8platform/serialize/v1"
)

// humanBytes форматирует байты в читаемый вид.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// humanDur форматирует секунды в «2ч 15м» / «45с».
func humanDur(sec int64) string {
	if sec < 0 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%dс", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dм %dс", int(d.Minutes())%60, int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dч %dм", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ago форматирует «сколько времени назад» от t (нулевое время → «—»).
func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return t.Format("15:04:05")
	case d < time.Minute:
		return fmt.Sprintf("%d с назад", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	}
}

// appIDLabel — человекочитаемое имя приложения сеанса по его app-id.
func appIDLabel(appID string) string {
	switch {
	case appID == "1CV8C":
		return "тонкий"
	case appID == "1CV8":
		return "толстый"
	case appID == "WebClient":
		return "веб"
	case appID == "Designer":
		return "конфигуратор"
	case appID == "JobScheduler":
		return "планировщик"
	case appID == "RAS":
		return "RAS"
	case appID == "SrvrConsole":
		return "консоль"
	case isJobString(appID):
		return "регл.задание"
	}
	return appID
}

func isJobString(appID string) bool {
	a := strings.ToLower(appID)
	return strings.Contains(a, "job") || strings.Contains(a, "фонов")
}

// portOf вытаскивает порт из адреса host:port (пусто, если не удалось).
func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return ""
}

// shortUUID обрезает UUID до первых 8 символов.
func shortUUID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// sessionTitle — однострочное представление сеанса в дереве.
func sessionTitle(s *serializev1.SessionInfo) string {
	user := s.GetUserName()
	if user == "" {
		user = "—"
	}
	title := fmt.Sprintf("%s %s@%s", s.GetAppId(), user, s.GetHost())
	if s.GetHibernate() {
		title += "  (спит)"
	}
	return title
}

// countUsersJobs считает по базе: уникальных пользователей (пустое имя —
// один «неизвестный» пользователь) и количество регламентных сеансов.
func countUsersJobs(sessions []*serializev1.SessionInfo, infobaseID string) (users, jobs int) {
	seen := map[string]bool{}
	for _, s := range sessions {
		if s.GetInfobaseId() != infobaseID {
			continue
		}
		if isJobSession(s.GetAppId()) {
			jobs++
			continue
		}
		seen[s.GetUserName()] = true // включая "" — неизвестный пользователь
	}
	return len(seen), jobs
}
