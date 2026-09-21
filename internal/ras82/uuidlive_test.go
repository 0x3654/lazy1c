package ras82

import (
	"os"
	"testing"
	"time"

	"lazy1c/internal/ras"
)

// TestUUIDStableAcrossPolls: uuid баз из полного опроса (FetchAll) должны
// совпадать с uuid из быстрого (FetchQuick), а сеансы — попадать в свои
// базы на ОБОИХ путях. Регрессия 2026-09: FetchAll uuid не собирал, базы
// получали NameUUID, сеансы с быстрого — настоящие → все сеансы осиротели.
// Живой стенд, только чтение: RA82TEST=yes go test -run TestUUIDStableAcrossPolls

// rasAddr — адрес живого 8.2-стенда для тестов: только из env RA82ADDR,
// без дефолта (рабочие адреса в репо не попадают).
func rasAddr() string { return os.Getenv("RA82ADDR") }

func TestUUIDStableAcrossPolls(t *testing.T) {
	if os.Getenv("RA82TEST") != "yes" {
		t.Skip("живой стенд — только с RA82TEST=yes")
	}
	c := NewConn82(rasAddr(), 20*time.Second)
	s1, err := c.Poll(nil, ras.Creds{}, ras.Creds{}, nil) // полный (FetchAll)
	if err != nil {
		t.Fatalf("poll1: %v", err)
	}
	s2, err := c.Poll(nil, ras.Creds{}, ras.Creds{}, nil) // быстрый (FetchQuick)
	if err != nil {
		t.Fatalf("poll2: %v", err)
	}
	cu1, cu2 := s1.Clusters[0].GetUuid(), s2.Clusters[0].GetUuid()

	check := func(tag string, s *ras.Snapshot, cu string) (orphans, real int) {
		known := map[string]bool{}
		for _, ib := range s.Infobases[cu] {
			known[ib.GetUuid()] = true
		}
		for _, ss := range s.Sessions[cu] {
			if !known[ss.GetInfobaseId()] {
				orphans++
				t.Errorf("%s: сеанс %s@%s без базы (InfobaseId=%s)", tag, ss.GetUserName(), ss.GetHost(), ss.GetInfobaseId())
			}
		}
		// NameUUID-детерминированные uuid в быстром ответе не должны встречаться:
		// если база вдруг без настоящего uuid, связь всё равно работает, но
		// между опросами uuid плавает — считаем отдельно
		for _, ib := range s.Infobases[cu] {
			if ib.GetUuid() == NameUUID(ib.GetName()) {
				real--
			}
		}
		return orphans, real
	}
	o1, r1 := check("полный", s1, cu1)
	o2, r2 := check("быстрый", s2, cu2)
	uuid1 := map[string]string{}
	for _, ib := range s1.Infobases[cu1] {
		uuid1[ib.GetName()] = ib.GetUuid()
	}
	diff := 0
	for _, ib := range s2.Infobases[cu2] {
		if u, ok := uuid1[ib.GetName()]; ok && u != ib.GetUuid() {
			diff++
			t.Errorf("uuid базы %s поплыл: %s → %s", ib.GetName(), u, ib.GetUuid())
		}
	}
	t.Logf("полный: баз=%d (без настоящего uuid: %d) сирот=%d | быстрый: баз=%d (без uuid: %d) сирот=%d | поплыло uuid=%d",
		len(s1.Infobases[cu1]), len(s1.Infobases[cu1])+r1, o1, len(s2.Infobases[cu2]), len(s2.Infobases[cu2])+r2, diff, o2)
	if o1 > 0 || o2 > 0 || diff > 0 {
		t.Fatal("связка сеанс↔база нестабильна между опросами")
	}
}
