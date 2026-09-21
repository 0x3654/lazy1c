package discover

import "testing"

func TestHosts(t *testing.T) {
	hs, err := Hosts("127.0.0.0/29")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 6 { // /29 = 8 адресов минус network и broadcast
		t.Fatalf("получил %d хостов, хочу 6: %v", len(hs), hs)
	}
	if hs[0].String() != "127.0.0.1" {
		t.Fatalf("первый хост %s, хочу 127.0.0.1", hs[0])
	}
	one, err := Hosts("127.0.0.1/32")
	if err != nil || len(one) != 1 {
		t.Fatalf("/32: %v %d", err, len(one))
	}
	if _, err := Hosts("не-cidr"); err == nil {
		t.Fatal("мусор должен давать ошибку")
	}
}
