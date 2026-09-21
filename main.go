// lazy1c — TUI для серверов администрирования 1С:Предприятие (RAS).
// Нативный протокол RAS по TCP: без rac, без docker; работает на macOS/Linux/Windows.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"lazy1c/internal/cli"
	"lazy1c/internal/config"
	"lazy1c/internal/discover"
	"lazy1c/internal/engine"
	"lazy1c/internal/ras"
	"lazy1c/internal/ui"
)

func ctx() context.Context { return context.Background() }

// runDiscover — скан сети на RAS-серверы 1С: `lazy1c discover [CIDR...] [-ports 1545,2545]`.
// Без аргументов — подсети своих интерфейсов. Находки проверяются протоколом RAS.
func runDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	portsF := fs.String("ports", "1545,2545", "порты RAS через запятую (на машине могут быть версии на разных портах)")
	timeoutF := fs.Int("timeout-ms", 400, "таймаут дозвона на хост, мс")
	fs.Parse(args)

	cidrs := fs.Args()
	if len(cidrs) == 0 {
		cidrs = discover.LocalCIDRs()
		if len(cidrs) == 0 {
			log.Fatal("не удалось определить подсети интерфейсов — укажите CIDR: lazy1c discover 192.168.1.0/24")
		}
		fmt.Printf("диапазоны не заданы — сканирую подсети интерфейсов: %v\n", cidrs)
	}
	var ports []int
	for _, p := range strings.Split(*portsF, ",") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err != nil {
			log.Fatalf("порт %q: %v", p, err)
		}
		ports = append(ports, n)
	}
	fmt.Printf("сканирую %v порты %v …\n", cidrs, ports)
	t0 := time.Now()
	res, err := discover.Scan(ctx(), cidrs, ports, time.Duration(*timeoutF)*time.Millisecond, func(r discover.Result) {
		fmt.Printf("  найден %s · %s · кластеров: %d\n", r.Addr, r.Version, r.Clusters)
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("готово за %s: RAS-серверов %d\n", time.Since(t0).Round(time.Millisecond), len(res))
	if len(res) > 0 {
		fmt.Println("\nдобавить: TUI «+» (имя/адрес) или [[cluster]] в lazy1c.toml")
	}
}

// initConfig ищет 1С-серверы в docker и создаёт lazy1c.toml (без перезаписи).
func initConfig() {
	clusters, err := discover.DockerClusters()
	if err != nil {
		fmt.Printf("не получилось опросить docker: %v\n", err)
		return
	}
	if len(clusters) == 0 {
		fmt.Println("запущенных контейнеров с RAS (порт 1545) не найдено.")
		fmt.Println("Создайте lazy1c.toml вручную:")
		fmt.Println()
		fmt.Println("  [[cluster]]")
		fmt.Println("  name    = \"prod\"")
		fmt.Println("  address = \"ras01.example.ru:1545\"")
		return
	}
	var b strings.Builder
	b.WriteString("# конфиг lazy1c — сгенерирован `lazy1c init`\n\n")
	b.WriteString("refresh_interval = 5\ncommand_timeout  = 10\n\n")
	for _, c := range clusters {
		fmt.Printf("найден: %s — %s\n", c.Name, c.Address)
		b.WriteString("[[cluster]]\n")
		fmt.Fprintf(&b, "name    = %q\n", c.Name)
		fmt.Fprintf(&b, "address = %q\n\n", c.Address)
	}
	if _, err := os.Stat("lazy1c.toml"); err == nil {
		fmt.Println("\nlazy1c.toml уже существует — не трогаю. Найденные кластеры:")
		fmt.Print(b.String())
		return
	}
	if err := os.WriteFile("lazy1c.toml", []byte(b.String()), 0o644); err != nil {
		fmt.Printf("не удалось записать lazy1c.toml: %v\n", err)
		return
	}
	fmt.Println("\nзаписан lazy1c.toml — запускайте lazy1c")
}

func main() {
	configPath := flag.String("config", "", "путь к lazy1c.toml")
	readOnly := flag.Bool("read-only", false, "только просмотр: запретить любые изменения")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "lazy1c — TUI администрирования 1С через RAS\n\n")
		fmt.Fprintf(os.Stderr, "Использование:\n  lazy1c          TUI-интерфейс\n  lazy1c dump     разовый дамп состояния кластеров\n  lazy1c version  версия\n\nФлаги:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.Arg(0) == "version" {
		fmt.Println("lazy1c dev")
		return
	}

	if flag.Arg(0) == "init" {
		initConfig()
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("%v\n\nПодсказка: lazy1c init найдёт 1С-серверы в docker и создаст конфиг", err)
	}
	if *readOnly {
		cfg.ReadOnly = true
	}

	switch flag.Arg(0) {
	case "", "tui":
		if err := ui.Run(ctx(), cfg); err != nil {
			log.Fatal(err)
		}
	case "dump":
		if err := dump(cfg); err != nil {
			log.Fatal(err)
		}
	case "ctl":
		os.Exit(cli.Run(cfg, flag.Args()[1:]))
	case "discover":
		runDiscover(flag.Args()[1:])
	case "init":
		// обработано выше, до загрузки конфига
	default:
		log.Fatalf("неизвестная команда %q", flag.Arg(0))
	}
}

func dump(cfg *config.Config) error {
	ctx := context.Background()
	for _, cc := range cfg.Clusters {
		fmt.Printf("════ %s (%s) ════\n", cc.Name, cc.Address)
		conn := engine.New(cc.Address, time.Duration(cfg.CommandTimeout)*time.Second, cc.Engine)
		snap, err := conn.Poll(ctx, ras.Creds{User: cc.User, Pwd: cc.Pwd}, ras.Creds{User: cc.IbUser, Pwd: cc.IbPwd}, nil)
		if err != nil {
			fmt.Printf("  НЕДОСТУПЕН: %v\n\n", err)
			continue
		}
		fmt.Printf("  опрошен за %s\n", snap.Took.Round(time.Millisecond))
		for k, e := range snap.ListErrs {
			fmt.Printf("  ! %s: %v\n", k, e)
		}
		for _, cl := range snap.Clusters {
			cid := cl.GetUuid()
			fmt.Printf("\n  Кластер %q  %s:%d  (%s)\n", cl.GetName(), cl.GetHost(), cl.GetPort(), short(cid))
			fmt.Printf("    баз: %d, сеансов: %d, соединений: %d, rphost: %d, блокировок: %d\n",
				len(snap.Infobases[cid]), len(snap.Sessions[cid]),
				len(snap.Connections[cid]), len(snap.Processes[cid]), len(snap.Locks[cid]))
			for i, ib := range snap.Infobases[cid] {
				if i >= 5 {
					fmt.Printf("      … и ещё %d\n", len(snap.Infobases[cid])-5)
					break
				}
				fmt.Printf("      • %-20s %s\n", ib.GetName(), short(ib.GetUuid()))
			}
			for _, s := range snap.Sessions[cid] {
				fmt.Printf("      ◆ %-12s %s@%s hib=%v\n",
					s.GetAppId(), s.GetUserName(), s.GetHost(), s.GetHibernate())
			}
		}
		fmt.Println()
	}
	_ = json.Marshal // placeholder: json-дамп добавится с TUI
	return nil
}

func short(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}
