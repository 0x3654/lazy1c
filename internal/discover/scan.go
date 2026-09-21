// Package discover — поиск RAS-серверов 1С в сети: TCP-скан порта 1545
// по CIDR-диапазонам с подтверждением находки протоколом RAS.
package discover

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"lazy1c/internal/ras"
)

// Result — найденный RAS-эндпоинт (проверен протоколом).
type Result struct {
	Addr      string // host:1545
	Version   string // версия платформы агента
	Clusters  int    // число кластеров на агенте
	ClusterID string // uuid первого кластера — личность сервера (дедуп по интерфейсам)
}

// LocalCIDRs — подсети своих интерфейсов (IPv4), по умолчанию для скана.
func LocalCIDRs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLoopback() {
				continue
			}
			out = append(out, ipn.String())
		}
	}
	return out
}

// Hosts — итерируемые адреса CIDR (без сетевого и широковещательного).
func Hosts(cidr string) ([]net.IP, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("%q: не CIDR", cidr)
	}
	ip = ip.To4()
	if ip == nil {
		return nil, fmt.Errorf("%q: только IPv4", cidr)
	}
	var out []net.IP
	for cur := ip.Mask(ipnet.Mask); ipnet.Contains(cur); incIP(cur) {
		c := make(net.IP, 4)
		copy(c, cur)
		out = append(out, c)
		if len(out) > 65536 {
			return nil, fmt.Errorf("%q: слишком большая сеть (>65k адресов), сузьте", cidr)
		}
	}
	if len(out) > 2 {
		out = out[1 : len(out)-1] // без network и broadcast
	}
	return out, nil
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

// Scan — просканировать CIDR-диапазоны по списку портов (по умолчанию
// 1545 и 2545: на одной машине могут жить две версии 1С на разных портах),
// открытые проверяются RAS-пробой (версия + кластеры).
// progress вызывается на каждую находку (может быть nil).
func Scan(ctx context.Context, cidrs []string, ports []int, timeout time.Duration, progress func(Result)) ([]Result, error) {
	if len(ports) == 0 {
		ports = []int{1545, 2545}
	}
	type hit struct {
		ip   net.IP
		spec string
	}
	var hits []hit
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 256) // параллелизм дозвона

	for _, cidr := range cidrs {
		hosts, err := Hosts(cidr)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			for _, port := range ports {
				wg.Add(1)
				go func(ip net.IP, port int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					addr := net.JoinHostPort(ip.String(), fmt.Sprint(port))
					d := net.Dialer{Timeout: timeout}
					c, err := d.DialContext(ctx, "tcp", addr)
					if err != nil {
						return
					}
					c.Close()
					mu.Lock()
					hits = append(hits, hit{ip, addr})
					mu.Unlock()
				}(h, port)
			}
		}
	}
	wg.Wait()

	// каждую находку проверяем протоколом RAS
	var out []Result
	seenIDs := map[string]bool{}
	for _, h := range hits {
		conn := ras.NewConn(h.spec, 3*time.Second)
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		v, clusters, err := conn.Discover(pctx)
		cancel()
		conn.Close()
		if err != nil {
			continue // порт открыт, но это не RAS
		}
		r := Result{Addr: h.spec, Version: v, Clusters: len(clusters)}
		if len(clusters) > 0 {
			r.ClusterID = clusters[0].GetUuid()
		}
		// один сервер, найденный через разные интерфейсы — один пункт
		if r.ClusterID != "" {
			if seenIDs[r.ClusterID] {
				continue
			}
			seenIDs[r.ClusterID] = true
		}
		out = append(out, r)
		if progress != nil {
			progress(r)
		}
	}
	return out, nil
}
