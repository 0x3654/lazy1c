package ui

import (
	"context"
	"os"
	"testing"
	"time"

	"lazy1c/internal/engine"
	"lazy1c/internal/ras"
)

// TestLiveMultiTerminate — живой прогон на ЛОКАЛЬНОМ стенде (docker server1c,
// host.docker.internal:1545→авто→MMC 8.3): завершить два тонких тест-клиента
// полным путём engine и убедиться, что они исчезли из свежего опроса.
// Запуск: docker run … -e LIVEKILL=yes go test -run TestLiveMultiTerminate.
func TestLiveMultiTerminate(t *testing.T) {
	if os.Getenv("LIVEKILL") != "yes" {
		t.Skip("живое завершение — только с LIVEKILL=yes")
	}
	conn := engine.New("host.docker.internal:1545", 15*time.Second, "auto")
	ctx := context.Background()
	snap, err := conn.Poll(ctx, ras.Creds{}, ras.Creds{}, nil)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	var cuuid string
	for c := range snap.Sessions {
		cuuid = c
		break
	}
	var victims []string
	for _, s := range snap.Sessions[cuuid] {
		if s.GetAppId() == "1CV8C" && len(victims) < 2 { // тонкие тест-клианты
			victims = append(victims, s.GetUuid())
			t.Logf("жертва: %s %s@%s", s.GetUuid()[:8], s.GetUserName(), s.GetHost())
		}
	}
	if len(victims) == 0 {
		t.Fatal("нет тонких клиентов для проверки")
	}
	v1, v2 := victims[0], ""
	if len(victims) > 1 {
		v2 = victims[1]
	}

	ids := []string{v1}
	if v2 != "" {
		ids = append(ids, v2)
	}
	for i, id := range ids {
		if err := conn.TerminateSession(ctx, cuuid, id, "живая проверка lazy1c"); err != nil {
			t.Fatalf("terminate #%d не прошёл: %v", i+1, err)
		}
		t.Logf("terminate #%d принят", i+1)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		snap2, err := conn.Poll(ctx, ras.Creds{}, ras.Creds{}, nil)
		if err != nil {
			t.Fatalf("re-poll: %v", err)
		}
		alive := map[string]bool{}
		for _, s := range snap2.Sessions[cuuid] {
			alive[s.GetUuid()] = true
		}
		if !alive[v1] && (v2 == "" || !alive[v2]) {
			t.Log("сеансы реально завершились ✓")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("сеансы живы после terminate: v1=%v v2=%v", alive[v1], alive[v2])
		}
		time.Sleep(2 * time.Second)
	}
}
