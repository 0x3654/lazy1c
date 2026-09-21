// mmmspy — прозрачный TCP-шпион между MMC-консолью 1С и ragent: пишет оба
// направления в hex-лог (формат парсится research/extract83.py).
// Петля снятия дампов описана в README («Снятие дампов»).
//
// Использование:
//
//	mmmspy -listen 10.211.55.2:3540 -target ragent:1540 -o research/spy83.log
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

func main() {
	listen := flag.String("listen", ":9540", "адрес прослушки")
	target := flag.String("target", "", "адрес ragent (обязательно)")
	out := flag.String("o", "", "файл лога (по умолчанию stdout)")
	flag.Parse()
	if *target == "" {
		flag.Usage()
		os.Exit(2)
	}
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		w = f
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(w, "mmmspy: слушаю %s → %s\n\n", *listen, *target)
	n := 0
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		n++
		id := n
		fmt.Fprintf(w, "[%s] CONN#%d открыт %s → %s\n\n",
			time.Now().Format("15:04:05.000"), id, c.RemoteAddr(), *target)
		go pipe(id, c, *target, w)
	}
}

// pipe тянет один коннект в обе стороны, логируя кадры с hex-дампом.
func pipe(id int, client net.Conn, target string, w io.Writer) {
	server, err := net.Dial("tcp", target)
	if err != nil {
		fmt.Fprintf(w, "[%s] CONN#%d ошибка цели: %v\n\n",
			time.Now().Format("15:04:05.000"), id, err)
		client.Close()
		return
	}
	var mu sync.Mutex
	c2s := logConn(id, "C→S", client, server, w, &mu)
	s2c := logConn(id, "S→C", server, client, w, &mu)
	done := make(chan struct{}, 2)
	go func() { c2s(); done <- struct{}{} }()
	go func() { s2c(); done <- struct{}{} }()
	<-done
	client.Close()
	server.Close()
	fmt.Fprintf(w, "[%s] CONN#%d закрыт\n\n", time.Now().Format("15:04:05.000"), id)
}

func logConn(id int, dir string, src, dst net.Conn, w io.Writer, mu *sync.Mutex) func() {
	return func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				mu.Lock()
				fmt.Fprintf(w, "[%s] CONN#%d %s %d байт:\n",
					time.Now().Format("15:04:05.000"), id, dir, n)
				for off := 0; off < n; off += 16 {
					end := min(off+16, n)
					hexs := ""
					asc := ""
					for _, b := range buf[off:end] {
						hexs += fmt.Sprintf("%02x ", b)
						if b >= 0x20 && b < 0x7f {
							asc += string(rune(b))
						} else {
							asc += "."
						}
					}
					fmt.Fprintf(w, "  %04x  %-48s|%s|\n", off, hexs, asc)
				}
				fmt.Fprintln(w)
				mu.Unlock()
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
}
