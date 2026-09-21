package ras82

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestKillLegacyOld — контрольный живой kill старого толстого клиента (00:40),
// жертва названа пользователем. Запуск: RA82KILL=yes go test -v -run TestKillLegacyOld
func TestKillLegacyOld(t *testing.T) {
	if rasAddr() == "" {
		t.Skip("живой стенд — RA82ADDR=host:1540")
	}
	if os.Getenv("RA82KILL") != "yes" {
		t.Skip("живой kill — только с RA82KILL=yes")
	}
	_, lists, err := DialAuto(rasAddr(), 20*time.Second)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	var victim, user string
	for _, s := range lists.Sessions {
		if s.InfobaseName == "op01_kabakov" && s.Started.Minute() == 40 {
			victim, user = s.ID, s.UserUUID
		}
	}
	if victim == "" {
		t.Skip("жертва 00:40 не найдена")
	}
	fmt.Println("жертва:", victim, "юзер:", user)

	cl, err := Dial(rasAddr(), 20*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	if _, err := cl.FetchAll(&prodFrames); err != nil {
		t.Fatalf("fetchall: %v", err)
	}
	if err := cl.TerminateWith(tmplBinTerminateProd, victim, user); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	t.Log("kill отправлен и принят")
}
