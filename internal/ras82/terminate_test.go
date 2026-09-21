package ras82

import (
	"os"
	"testing"
	"time"

	"lazy1c/internal/ras"
)

// TestTerminateDeadSession — полный путь Conn82: Poll (наполняет userBySess)
// затем TerminateSession по УЖЕ мёртвому сеансу (Designer 4057e092, убит MMC
// 7.09.2026). Никого не убивает. Запуск: RA82TEST=yes go test -v -run TestTerminateDeadSession
func TestTerminateDeadSession(t *testing.T) {
	if rasAddr() == "" {
		t.Skip("живой стенд — RA82ADDR=host:1540")
	}
	if os.Getenv("RA82TEST") != "yes" {
		t.Skip("живой стенд — только с RA82TEST=yes")
	}
	conn := NewConn82(rasAddr(), 20*time.Second)
	snap, err := conn.Poll(nil, ras.Creds{}, ras.Creds{}, nil)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	t.Logf("в карте userBySess: %d записей", len(conn.userBySess))
	for s, u := range conn.userBySess {
		t.Logf("  %s → %s", s[:8], u[:8])
	}
	_ = snap
	if err := conn.TerminateSession(nil, "", "4057e092-0634-4b24-9ed0-7a270174b26a", "test"); err != nil {
		t.Fatalf("terminate по мёртвому: %v", err)
	} else {
		t.Log("terminate принят")
	}
}
