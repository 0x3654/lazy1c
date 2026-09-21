package mmc83

import (
	"os"
	"testing"
	"time"
)

// TestLive83 — полный путь против домашнего контейнера (RAS выключен,
// только ragent:1540). Чтение. RA83TEST=host.docker.internal:1540.
func TestLive83(t *testing.T) {
	addr := os.Getenv("RA83TEST")
	if addr == "" {
		t.Skip("живой стенд — RA83TEST=host:1540")
	}
	c, err := Dial(addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cls, err := c.Clusters()
	if err != nil {
		t.Fatal("кластеры:", err)
	}
	for _, cl := range cls {
		t.Logf("кластер %s %q %s:%d", cl.Uuid, cl.Name, cl.Host, cl.Port)
	}
	if len(cls) == 0 {
		t.Fatal("кластеры не найдены")
	}
	bases, err := c.Bases()
	if err != nil {
		t.Fatal("базы:", err)
	}
	for _, b := range bases {
		t.Logf("база %-18s uuid=%s", b.Name, b.Uuid)
	}
	sess, err := c.Sessions(bases)
	if err != nil {
		t.Fatal("сеансы:", err)
	}
	for _, s := range sess {
		t.Logf("сеанс: база=%s юзер=%s хост=%s прил=%s uuid=%s", s.InfobaseName, s.User, s.Host, s.App, s.Uuid)
	}
	if len(sess) == 0 {
		t.Log("сеансов нет (живых пользовательских сеансов может не быть)")
	}
}
