#!/usr/bin/env python3
# Сводка по кадрам: строки 9a/97, uuid-теги 95/d5, флаги, текстовые кортежи.
import sys, os, re, glob

def strings_9a(d):
    out = []
    i = 0
    while i < len(d) - 2:
        if d[i] == 0x9A:
            ln = d[i+1]
            if 0 < ln <= 60 and i + 2 + ln <= len(d):
                try:
                    out.append(d[i+2:i+2+ln].decode("utf-8"))
                    i += 2 + ln
                    continue
                except UnicodeDecodeError:
                    pass
        i += 1
    return out

def text_tuples(d):
    m = re.findall(rb"\{[0-9],[0-9a-f\-]{36},[0-9,]+\}", d)
    return [x.decode() for x in m[:6]]

for f in sorted(glob.glob("research/spy83-frames/*S2C*.bin")) + sorted(glob.glob("research/spy83-frames/*C2S*.bin")):
    if "_4.bin" in f: continue
    d = open(f, "rb").read()
    ss = strings_9a(d)
    tt = text_tuples(d)
    name = os.path.basename(f)
    head = d[:2].hex()
    print(f"== {name} [{head}] {len(d)}Б")
    if ss: print("   строки:", [s for s in ss if len(s) > 1][:14])
    if tt: print("   кортежи:", tt[:3])
