# -*- coding: utf-8 -*-
"""从 AutoClaw app.asar 中按关键字抓取 promotion-reward / 积分加油站领取调用现场。
asar 格式：文件内容按偏移存储，JS 是明文（utf8），直接整文件扫。
"""
import os, re, sys

ASAR = r"D:\autoclaw\resources\app.asar"
OUT = r"D:\wild-work\src-true\_probe\asar_promotion_hits.txt"

KEYWORDS = [
    b"promotion-reward",
    b"credits_station",
    b"Token_Rush",
    b"modal_id",
    b"reward_type",
    b"sendReward",
    b"timingReward",
    b"identity-tasks/complete",
    b"web-event/report",
]

def main():
    raw = open(ASAR, "rb").read()
    lines = []
    for kw in KEYWORDS:
        positions = []
        start = 0
        while True:
            i = raw.find(kw, start)
            if i < 0:
                break
            positions.append(i)
            start = i + 1
            if len(positions) > 40:
                break
        lines.append("#" * 10 + f" kw={kw.decode()} hits={len(positions)}")
        for i in positions[:12]:
            ctx = raw[max(0, i - 400): i + 400]
            text = ctx.decode("utf-8", errors="replace")
            text = text.replace("\n", "\\n").replace("\r", "")
            lines.append(f"--- @offset {i} ---")
            lines.append(text)
        lines.append("")
    open(OUT, "w", encoding="utf-8").write("\n".join(lines))
    print(f"written {OUT}")

if __name__ == "__main__":
    main()
