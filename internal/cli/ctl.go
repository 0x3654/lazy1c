// Package cli — неинтерактивное управление кластерами (lazy1c ctl) для скриптов.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	messagesv1 "github.com/v8platform/protos/gen/ras/messages/v1"
	"lazy1c/internal/config"
	"lazy1c/internal/engine"
	"lazy1c/internal/ras"
)

// Run выполняет `lazy1c ctl <команда> [флаги]` и выходит.
func Run(cfg *config.Config, args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet("ctl "+cmd, flag.ExitOnError)
	clusterF := fs.String("cluster", "", "имя кластера из конфига (пусто = все)")
	baseF := fs.String("base", "", "имя информационной базы")
	yesF := fs.Bool("yes", false, "подтвердить изменение (для скриптов)")
	switch cmd {
	case "clusters":
	case "bases":
	case "sessions":
	case "terminate":
		sessionF := fs.String("session", "", "UUID сеанса")
		sleepingF := fs.Bool("sleeping", false, "завершить все спящие")
		fs.Parse(rest)
		return terminate(cfg, *clusterF, *sessionF, *sleepingF, *yesF)
	case "jobs":
		onF := fs.Bool("on", false, "запретить регламентные задания")
		offF := fs.Bool("off", false, "разрешить регламентные задания")
		fs.Parse(rest)
		val, ok := boolSelect(*onF, *offF)
		return denyToggle(cfg, *clusterF, *baseF, "jobs", val, ok, *yesF)
	case "lock":
		onF := fs.Bool("on", false, "запретить начало сеансов")
		offF := fs.Bool("off", false, "разрешить начало сеансов")
		fs.Parse(rest)
		val, ok := boolSelect(*onF, *offF)
		return denyToggle(cfg, *clusterF, *baseF, "lock", val, ok, *yesF)
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда ctl: %q\n\n", cmd)
		usage()
		return 2
	}
	fs.Parse(rest)
	return read(cfg, cmd, *clusterF, *baseF)
}

func onOffCli(v bool) string {
	if v {
		return "запрещено"
	}
	return "разрешено"
}

func boolSelect(on, off bool) (val bool, ok bool) {
	switch {
	case on && !off:
		return true, true
	case off && !on:
		return false, true
	}
	return false, false
}

func usage() {
	fmt.Fprint(os.Stderr, `lazy1c ctl — неинтерактивное управление (для скриптов/CI)

Команды:
  lazy1c ctl clusters                          кластеры из конфига
  lazy1c ctl bases    [-cluster ИМЯ]           информационные базы
  lazy1c ctl sessions [-cluster ИМЯ] [-base БАЗА]   сеансы (uuid, приложение, пользователь)
  lazy1c ctl terminate -cluster ИМЯ -session UUID [-yes]          завершить сеанс
  lazy1c ctl terminate -cluster ИМЯ -sleeping [-yes]              завершить все спящие
  lazy1c ctl jobs  -cluster ИМЯ -base БАЗА -on|-off [-yes]        запрет/разрешение регл. заданий
  lazy1c ctl lock -cluster ИМЯ -base БАЗА -on|-off [-yes]         запрет/разрешение начала сеансов
`)
}

// dial — соединения к выбранным кластерам (по имени или все).
func dial(cfg *config.Config, name string) ([]engine.Conn, []ras.Creds, []ras.Creds, []string, error) {
	timeout := time.Duration(cfg.CommandTimeout) * time.Second
	var conns []engine.Conn
	var creds []ras.Creds
	var ibCreds []ras.Creds
	var names []string
	for _, c := range cfg.Clusters {
		if name != "" && c.Name != name {
			continue
		}
		conns = append(conns, engine.New(c.Address, timeout, c.Engine))
		creds = append(creds, ras.Creds{User: c.User, Pwd: c.Pwd})
		ibCreds = append(ibCreds, ras.Creds{User: c.IbUser, Pwd: c.IbPwd})
		names = append(names, c.Name)
	}
	if len(conns) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("кластер %q не найден в конфиге", name)
	}
	return conns, creds, ibCreds, names, nil
}

func read(cfg *config.Config, cmd, clusterName, baseName string) int {
	conns, creds, ibCreds, names, err := dial(cfg, clusterName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx := context.Background()
	rc := 0
	for i, conn := range conns {
		snap, err := conn.Poll(ctx, creds[i], ibCreds[i], nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: недоступен: %v\n", names[i], err)
			rc = 1
			continue
		}
		for _, cl := range snap.Clusters {
			switch cmd {
			case "clusters":
				fmt.Printf("%s\t%s:%d\t%s\tбаз:%d\n", names[i], cl.GetHost(), cl.GetPort(),
					cl.GetUuid(), len(snap.Infobases[cl.GetUuid()]))
			case "bases":
				for _, ib := range snap.Infobases[cl.GetUuid()] {
					if baseName != "" && ib.GetName() != baseName {
						continue
					}
					// флаги запретов, если карточка прочиталась
					jobs, lock := "-", "-"
					if info := snap.IBInfo[cl.GetUuid()][ib.GetUuid()]; info != nil {
						jobs, lock = onOffCli(info.GetScheduledJobsDeny()), onOffCli(info.GetSessionsDeny())
					}
					fmt.Printf("%s\t%s\t%s\tРЗ:%s\tВход:%s\n", names[i], ib.GetName(), ib.GetUuid(), jobs, lock)
				}
			case "sessions":
				ibName := map[string]string{}
				for _, ib := range snap.Infobases[cl.GetUuid()] {
					ibName[ib.GetUuid()] = ib.GetName()
				}
				for _, s := range snap.Sessions[cl.GetUuid()] {
					b := ibName[s.GetInfobaseId()]
					if baseName != "" && b != baseName {
						continue
					}
					hib := ""
					if s.GetHibernate() {
						hib = "\tспит"
					}
					fmt.Printf("%s\t%s\t%s\t%s@%s\t%s%s\n", names[i], s.GetUuid(),
						s.GetAppId(), s.GetUserName(), s.GetHost(), b, hib)
				}
			}
		}
	}
	return rc
}

func terminate(cfg *config.Config, clusterName, sessionID string, sleeping, yes bool) int {
	if clusterName == "" || (sessionID == "" && !sleeping) {
		fmt.Fprintln(os.Stderr, "укажите -cluster и -session UUID (или -sleeping)")
		return 2
	}
	if !yes {
		fmt.Fprintln(os.Stderr, "изменение состояния кластера: добавьте -yes")
		return 2
	}
	conns, creds, ibCreds, names, err := dial(cfg, clusterName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, cr, ibcr := conns[0], creds[0], ibCreds[0]
	snap, err := conn.Poll(ctx, cr, ibcr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: недоступен: %v\n", names[0], err)
		return 1
	}
	for _, cl := range snap.Clusters {
		var ids []string
		for _, s := range snap.Sessions[cl.GetUuid()] {
			if sessionID != "" {
				if s.GetUuid() == sessionID {
					ids = append(ids, sessionID)
				}
				continue
			}
			if sleeping && s.GetHibernate() {
				ids = append(ids, s.GetUuid())
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := conn.TerminateSession(ctx, cl.GetUuid(), id, "завершён из lazy1c ctl"); err != nil {
				fmt.Fprintf(os.Stderr, "%s: terminate %s: %v\n", names[0], id[:8], err)
				return 1
			}
			fmt.Printf("%s: завершен %s\n", names[0], id[:8])
		}
		if len(ids) == 0 {
			fmt.Printf("%s: подходящих сеансов нет\n", names[0])
		}
	}
	return 0
}

func denyToggle(cfg *config.Config, clusterName, baseName, what string, on, ok, yes bool) int {
	if !yes {
		fmt.Fprintln(os.Stderr, "изменение состояния кластера: добавьте -yes")
		return 2
	}
	if clusterName == "" || baseName == "" || !ok {
		fmt.Fprintln(os.Stderr, "укажите -cluster, -base и ровно один флаг -on|-off")
		return 2
	}
	conns, creds, ibCreds, names, err := dial(cfg, clusterName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, cr, ibcr := conns[0], creds[0], ibCreds[0]
	snap, err := conn.Poll(ctx, cr, ibcr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: недоступен: %v\n", names[0], err)
		return 1
	}
	for _, cl := range snap.Clusters {
		var ibID string
		for _, ib := range snap.Infobases[cl.GetUuid()] {
			if strings.EqualFold(ib.GetName(), baseName) {
				ibID = ib.GetUuid()
			}
		}
		if ibID == "" {
			fmt.Fprintf(os.Stderr, "%s: база %q не найдена\n", names[0], baseName)
			return 1
		}
		info, err := conn.GetInfobase(ctx, cl.GetUuid(), ibID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: свойства базы: %v\n", names[0], err)
			return 1
		}
		switch what {
		case "jobs":
			info.ScheduledJobsDeny = on
		case "lock":
			info.SessionsDeny = on
		}
		if err := conn.UpdateInfobase(ctx, &messagesv1.UpdateInfobaseRequest{ClusterId: cl.GetUuid(), Info: info}); err != nil {
			fmt.Fprintf(os.Stderr, "%s: обновление базы: %v\n", names[0], err)
			return 1
		}
		fmt.Printf("%s/%s: обновлено\n", names[0], baseName)
	}
	return 0
}
