#!/usr/bin/env python3
# Разбор hex-лога mmmspy на отдельные кадры + сводка.
import re, sys, os
log = open(sys.argv[1] if len(sys.argv) > 1 else "research/spy83-local.log").read()
outdir = sys.argv[2] if len(sys.argv) > 2 else "research/spy83-frames"
os.makedirs(outdir, exist_ok=True)
frame_re = re.compile(r"\[(\d\d:\d\d:\d\d\.\d+)\] CONN#(\d+) ([CS])→[CS] (\d+) байт:\n((?:  [0-9a-f]{4}  .*\n)+)", re.M)
n = 0
for m in frame_re.finditer(log):
    ts, conn, direction, size, hexblock = m.groups()
    data = b""
    for line in hexblock.splitlines():
        m2 = re.match(r"\s*([0-9a-f]{4})\s{2}((?:[0-9a-f]{2} )*)", line)
        if not m2:
            continue
        data += bytes.fromhex(m2.group(2).replace(" ", ""))
    assert len(data) == int(size), f"кадр {n}: {len(data)} != {size}"
    tag = "C2S" if direction == "C" else "S2C"
    name = f"{n:02d}_{ts.replace(':','').replace('.','')}_C{conn}_{tag}_{size}.bin"
    open(os.path.join(outdir, name), "wb").write(data)
    n += 1
print(f"извлечено кадров: {n} → {outdir}/")
