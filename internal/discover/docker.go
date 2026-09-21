// Package discover ищет доступные RAS-серверы 1С (пока — в локальном docker).
package discover

import (
	"fmt"
	"os/exec"
	"strings"

	"lazy1c/internal/config"
)

// DockerClusters находит запущенные контейнеры серверов 1С: ищет проброску
// порта RAS (…->1545/tcp) и возвращает кластеры с адресом localhost:<порт>.
func DockerClusters() ([]config.Cluster, error) {
	out, err := exec.Command("docker", "ps", "--format",
		"{{.Names}}\t{{.Image}}\t{{.Ports}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	var found []config.Cluster
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name, image, ports := parts[0], parts[1], parts[2]
		hostPort := hostPortForRAS(ports)
		if hostPort == "" {
			continue
		}
		addr := "localhost:" + hostPort
		if seen[addr] {
			continue
		}
		seen[addr] = true
		found = append(found, config.Cluster{
			Name:    clusterName(name, image),
			Address: addr,
		})
	}
	return found, nil
}

// hostPortForRAS вытаскивает хостовый порт, проброшенный на 1545/tcp контейнера.
func hostPortForRAS(ports string) string {
	for _, p := range strings.Split(ports, ", ") {
		// формат docker ps: "0.0.0.0:2545->1545/tcp, [::]:2545->1545/tcp"
		if !strings.HasSuffix(p, "->1545/tcp") {
			continue
		}
		addr := strings.TrimSuffix(p, "->1545/tcp") // "0.0.0.0:2545"
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			return addr[i+1:]
		}
	}
	return ""
}

// clusterName делает человекочитаемое имя: версия платформы из тега образа,
// иначе имя контейнера.
func clusterName(container, image string) string {
	if i := strings.LastIndex(image, ":"); i >= 0 && i+1 < len(image) {
		return image[i+1:]
	}
	return container
}
