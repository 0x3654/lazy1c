package ras82

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Узлы бинарной сериализации 1С (8.2, канал данных ragent).
// Грамматика эмпирическая, по захватам research/spy3.log:
//
//	81            — «нет значения» (null/undef/false-подобное)
//	82/84/87/8b …  — малые целые/флаги (значение кодируется紧跟 тегом при >127)
//	91 <i64 LE>   — дата: 100-мкс тики с 0001-01-01
//	95 <16Б>      — uuid (GUID-LE)
//	97 <len> <utf16le> — строка UTF-16LE
//	9a <len> <utf8>    — строка UTF-8
//	9d <varint>   — ссылка на значение из словаря потока
//	a1/a2/a3      — маркеры конца (значение/список/структура)
type node struct {
	kind string // "nil","int","time","uuid","str","list","end","ref","raw"
	i    int64
	t    time.Time
	s    string
	list []node
	ref  int
}

// dict накапливает все декодированные значения для обратных ссылок 9d.
type parser struct {
	b    []byte
	i    int
	dict []node
}

func parseAll(b []byte) []node {
	p := &parser{b: b}
	var out []node
	for p.i < len(p.b) {
		n, ok := p.value()
		if !ok {
			break
		}
		out = append(out, n)
	}
	return out
}

func (p *parser) byteAt() (byte, bool) {
	if p.i >= len(p.b) {
		return 0, false
	}
	return p.b[p.i], true
}

// value декодирует одно значение; end-маркеры возвращаются как узел kind=end.
func (p *parser) value() (node, bool) {
	c, ok := p.byteAt()
	if !ok {
		return node{}, false
	}
	switch {
	case c == 0x81:
		p.i++
		n := node{kind: "nil"}
		p.dict = append(p.dict, n)
		return n, true
	case c >= 0x82 && c <= 0xbf: // малое целое/флаг: значение кодируется самим тегом
		p.i++
		n := node{kind: "int", i: int64(c) - 0x82}
		p.dict = append(p.dict, n)
		return n, true
	case c == 0x91: // дата
		if p.i+9 > len(p.b) {
			return node{}, false
		}
		ticks := int64(binary.LittleEndian.Uint64(p.b[p.i+1 : p.i+9]))
		p.i += 9
		n := node{kind: "time", t: date1.Add(time.Duration(ticks) * 100 * time.Microsecond)}
		p.dict = append(p.dict, n)
		return n, true
	case c == 0x95: // uuid
		if p.i+17 > len(p.b) {
			return node{}, false
		}
		u := leGUID(p.b[p.i+1 : p.i+17])
		p.i += 17
		n := node{kind: "uuid", s: u}
		p.dict = append(p.dict, n)
		return n, true
	case c == 0x97: // utf16-строка
		if p.i+2 > len(p.b) {
			return node{}, false
		}
		nb := int(p.b[p.i+1]) // длина в байтах
		if p.i+2+nb > len(p.b) {
			return node{}, false
		}
		s := decodeUTF16(p.b[p.i+2 : p.i+2+nb])
		p.i += 2 + nb
		n := node{kind: "str", s: s}
		p.dict = append(p.dict, n)
		return n, true
	case c == 0x9a: // utf8-строка
		if p.i+2 > len(p.b) {
			return node{}, false
		}
		nb := int(p.b[p.i+1])
		if p.i+2+nb > len(p.b) {
			return node{}, false
		}
		s := string(p.b[p.i+2 : p.i+2+nb])
		p.i += 2 + nb
		n := node{kind: "str", s: s}
		p.dict = append(p.dict, n)
		return n, true
	case c == 0x9d: // ссылка на словарь
		p.i++
		v, vi := p.varint()
		n := node{kind: "ref", ref: int(v)}
		_ = vi
		return n, true
	case c == 0xa1 || c == 0xa2 || c == 0xa3:
		p.i++
		return node{kind: "end", i: int64(c)}, true
	case c == 0xa5: // начало структуры/списка? — собираем вложенные
		p.i++
		n := node{kind: "list"}
		p.dict = append(p.dict, n)
		for p.i < len(p.b) {
			inner, ok := p.value()
			if !ok {
				break
			}
			if inner.kind == "end" {
				break
			}
			n.list = append(n.list, inner)
		}
		return n, true
	default:
		// неизвестный тег — пропускаем байт, не ломаясь
		p.i++
		n := node{kind: "raw", i: int64(c)}
		return n, true
	}
}

func (p *parser) varint() (uint64, int) {
	var v uint64
	var shift uint
	start := p.i
	for p.i < len(p.b) && shift < 63 {
		c := p.b[p.i]
		p.i++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			break
		}
		shift += 7
	}
	return v, p.i - start
}

// resolve раскручивает ссылки 9d в значения словаря (один уровень; рекурсия не наблюдается).
func (p *parser) resolve(n node) node {
	if n.kind == "ref" && n.ref >= 0 && n.ref < len(p.dict) {
		return p.dict[n.ref]
	}
	return n
}

// date1 — эпоха 1С: 0001-01-01 00:00:00 UTC.
var date1 = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

func decodeUTF16(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		sb.WriteRune(rune(b[i]) | rune(b[i+1])<<8)
	}
	return sb.String()
}

// dumpNodes — отладочная печать дерева узлов.
func dumpNodes(ns []node, p *parser, indent int) string {
	var sb strings.Builder
	pad := strings.Repeat("  ", indent)
	for _, n := range ns {
		switch n.kind {
		case "list":
			sb.WriteString(fmt.Sprintf("%s[\n", pad))
			sb.WriteString(dumpNodes(n.list, p, indent+1))
			sb.WriteString(pad + "]\n")
		case "end":
			sb.WriteString(fmt.Sprintf("%s<end %02x>\n", pad, n.i))
		case "ref":
			r := p.resolve(n)
			sb.WriteString(fmt.Sprintf("%s->#%d = %s %v\n", pad, n.ref, r.kind, nodeText(r)))
		default:
			sb.WriteString(fmt.Sprintf("%s%s %v\n", pad, n.kind, nodeText(n)))
		}
	}
	return sb.String()
}

func nodeText(n node) string {
	switch n.kind {
	case "time":
		return n.t.Format("02.01.2006 15:04:05")
	case "uuid", "str":
		return n.s
	case "int":
		return fmt.Sprintf("%d", n.i)
	case "nil":
		return "∅"
	}
	return ""
}
