#!/usr/bin/env python3
"""Capture meeting-style notifications from the macOS notification DB (M0 spike).

Polls read-only, writes JSONL to ../captures/meetings.jsonl.
Privacy: only Outlook/Teams/Calendar apps are considered. Rows that look like
meeting reminders are stored in full. Everything else from those apps is stored
redacted (app, time, text lengths only).

Usage: capture.py [--once] [--interval 2]
"""
import argparse, datetime, json, os, plistlib, re, sqlite3, sys, time

DB = os.path.expanduser("~/Library/Group Containers/group.com.apple.usernoted/db2/db")
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "captures", "meetings.jsonl")
STATE = OUT + ".state"
APP_RE = re.compile(r"outlook|teams|ical|calendar", re.I)
MEET_RE = re.compile(r"meeting|starts?\b|starting|join|calendar|invit|reminder|\bin \d+ ?min", re.I)
EPOCH = 978307200  # 2001-01-01 -> unix


def load_last():
    try:
        return int(open(STATE).read().strip())
    except Exception:
        return None


def save_last(n):
    with open(STATE, "w") as f:
        f.write(str(n))


def poll(conn, last):
    rows = conn.execute(
        "select r.rec_id, a.identifier, r.delivered_date, r.data from record r "
        "join app a using(app_id) where r.rec_id > ? order by r.rec_id", (last,)).fetchall()
    out = []
    for rid, app, delivered, blob in rows:
        if not APP_RE.search(app or ""):
            continue
        try:
            req = plistlib.loads(blob).get("req", {})
        except Exception as e:
            req = {"_error": str(e)}
        titl, subt, body = req.get("titl"), req.get("subt"), req.get("body")
        text = " ".join(x for x in (titl, subt, body) if isinstance(x, str))
        ts = datetime.datetime.fromtimestamp((delivered or 0) + EPOCH).astimezone().isoformat()
        rec = {"rec_id": rid, "app": app, "delivered": ts, "category": req.get("cate")}
        if MEET_RE.search(text) or "ical" in (app or ""):
            rec.update(kind="meeting_like", title=titl, subtitle=subt, body=body)
        else:
            rec.update(kind="redacted", title_len=len(titl or ""), body_len=len(body or ""))
        out.append(rec)
    return rows[-1][0] if rows else last, out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--once", action="store_true")
    ap.add_argument("--interval", type=float, default=2.0)
    args = ap.parse_args()
    os.makedirs(os.path.dirname(os.path.abspath(OUT)), exist_ok=True)
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=5)
    last = load_last()
    if last is None:  # start from now; don't dump history
        last = conn.execute("select coalesce(max(rec_id),0) from record").fetchone()[0]
        save_last(last)
    while True:
        try:
            conn.close()  # fresh connection per poll: avoid stale WAL snapshots
            conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=5)
            last, recs = poll(conn, last)
            if recs:
                with open(OUT, "a") as f:
                    for r in recs:
                        f.write(json.dumps(r, ensure_ascii=False) + "\n")
            save_last(last)
        except sqlite3.Error as e:
            print("db error:", e, file=sys.stderr)
        if args.once:
            break
        time.sleep(args.interval)


if __name__ == "__main__":
    main()
