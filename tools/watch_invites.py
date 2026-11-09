#!/usr/bin/env python3
"""Watch the macOS notification DB for meeting invites and record only their structure (task 47).

Read-only. For every Outlook, Teams or Calendar notification that looks like an invite, it stores: the app, the time it arrived,
the category, the lengths of the title, subtitle and body, the Teams conversation kind, and a "shape" of the text in which every
word is replaced by `w` except weekday and month names, numbers, clock times and a few invite words. So a body like
"Ana invited you to Sprint planning Mon, Nov 16 10:00 AM" is kept as "w invited w w w w Mon, Nov 16 10:00 AM": enough to write a
time parser, nothing about who or what the meeting is.

Prints one line per invite and, once TARGET have been seen, a line starting ENOUGH, then exits (--keep-going to continue).

Usage: watch_invites.py [--target 5] [--interval 5] [--backfill-days 14] [--keep-going] [--summary]
"""
import argparse, datetime, json, os, plistlib, re, sqlite3, sys, time

DB = os.path.expanduser("~/Library/Group Containers/group.com.apple.usernoted/db2/db")
OUT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "private", "captures")
OUT = os.path.join(OUT_DIR, "invites.jsonl")
STATE = os.path.join(OUT_DIR, "invites.state")
EPOCH = 978307200  # 2001-01-01 in Unix seconds

APP_RE = re.compile(r"outlook|teams|ical|calendar", re.I)
INVITE_RE = re.compile(
    r"invit|meeting request|new time|proposed|rsvp|has been (updated|cancel)|updated:|cancel(l)?ed:|"
    r"accepted:|declined:|tentative|\bjoin (the )?meeting|scheduled (a )?meeting", re.I)
KEEP = re.compile(
    r"^(mon|tue|wed|thu|fri|sat|sun|jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec|am|pm|today|tomorrow|tonight|"
    r"invited|invitation|updated|canceled|cancelled|accepted|declined|tentative|proposed|new|time|meeting|request|"
    r"starts?|starting|at|on|from|to|in|minutes?|mins?|hours?|days?)[a-z]*[,.:]*$", re.I)
NUMBER = re.compile(r"^[\d:/\-.,–—]+[a-z]{0,2}[,.:]*$", re.I)


def shape(text):
    if not isinstance(text, str):
        return None
    out = []
    for token in text.split():
        bare = token.strip("()[]\"'")
        out.append(token if (NUMBER.match(bare) or KEEP.match(bare)) and len(bare) <= 12 else "w")
    return " ".join(out)


def archive_strings(blob):
    """The top-level string values of an NSKeyedArchiver archive (as in internal/ingest/thread.go)."""
    try:
        pl = plistlib.loads(blob)
        objs = pl["$objects"]
        root = objs[pl["$top"]["root"].data]
        return {objs[k.data]: objs[v.data] for k, v in zip(root["NS.keys"], root["NS.objects"])
                if isinstance(objs[k.data], str) and isinstance(objs[v.data], str)}
    except Exception:
        return {}


def thread_kind(usda):
    if not isinstance(usda, (bytes, bytearray)):
        return None
    conv = archive_strings(usda).get("default_id", "")
    if conv.startswith("19:meeting_"):
        return "meeting"
    if conv.endswith("@unq.gbl.spaces") or conv.startswith("8:"):
        return "direct"
    if conv.endswith("@thread.tacv2"):
        return "channel"
    if conv.endswith("@thread.v2") or conv.endswith("@thread.skype"):
        return "group"
    return None


def read_state():
    try:
        return int(open(STATE).read().strip())
    except Exception:
        return None


def count_captured():
    try:
        return sum(1 for _ in open(OUT))
    except FileNotFoundError:
        return 0


def connect():
    return sqlite3.connect(f"file:{DB}?mode=ro", uri=True, timeout=5)


def poll(last):
    conn = connect()  # a fresh connection each time avoids a stale WAL snapshot
    try:
        rows = conn.execute(
            "select r.rec_id, a.identifier, r.delivered_date, r.data from record r join app a using(app_id) "
            "where r.rec_id > ? order by r.rec_id", (last,)).fetchall()
    finally:
        conn.close()
    found, newest = [], last
    for rid, app, delivered, blob in rows:
        newest = max(newest, rid)
        if not APP_RE.search(app or ""):
            continue
        try:
            req = plistlib.loads(blob).get("req", {})
        except Exception:
            continue
        titl, subt, body = req.get("titl"), req.get("subt"), req.get("body")
        text = " ".join(x for x in (titl, subt, body) if isinstance(x, str))
        thread = thread_kind(req.get("usda"))
        # A message inside a meeting's chat is not an invite, so the thread kind is only recorded, never a reason to count it.
        if not (INVITE_RE.search(text) or "invit" in (req.get("cate") or "").lower()):
            continue
        when = datetime.datetime.fromtimestamp((delivered or 0) + EPOCH).astimezone().isoformat()
        found.append({
            "rec_id": rid, "app": app, "delivered": when, "category": req.get("cate"), "thread": thread,
            "lens": [len(titl or ""), len(subt or ""), len(body or "")],
            "shape": {"title": shape(titl), "subtitle": shape(subt), "body": shape(body)},
        })
    return newest, found


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", type=int, default=5)
    ap.add_argument("--interval", type=float, default=5.0)
    ap.add_argument("--backfill-days", type=float, default=14.0, help="on the first run, also look this far back")
    ap.add_argument("--keep-going", action="store_true")
    ap.add_argument("--summary", action="store_true", help="print how many have been captured and exit")
    args = ap.parse_args()
    os.makedirs(OUT_DIR, exist_ok=True)
    if args.summary:
        print(f"{count_captured()} invite-like notifications captured so far in {os.path.relpath(OUT)}")
        return
    last = read_state()
    if last is None:
        cutoff = (time.time() - args.backfill_days * 86400) - EPOCH
        conn = connect()
        row = conn.execute("select coalesce(min(rec_id) - 1, 0) from record where delivered_date >= ?", (cutoff,)).fetchone()
        conn.close()
        last = row[0]
    seen = {json.loads(l)["rec_id"] for l in open(OUT)} if os.path.exists(OUT) else set()
    announced = False
    while True:
        try:
            last, found = poll(last)
        except sqlite3.Error as e:
            print("db error:", e, file=sys.stderr)
            found = []
        with open(STATE, "w") as f:
            f.write(str(last))
        for rec in found:
            if rec["rec_id"] in seen:
                continue
            seen.add(rec["rec_id"])
            with open(OUT, "a") as f:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
            print(f"invite {len(seen)}/{args.target}: {rec['app']} category={rec['category']} thread={rec['thread']} "
                  f"lens={rec['lens']}", flush=True)
        if len(seen) >= args.target and not announced:
            announced = True
            print(f"ENOUGH: {len(seen)} invite-like notifications captured in {os.path.relpath(OUT)}; "
                  "time to start task 47", flush=True)
            if not args.keep_going:
                return
        time.sleep(args.interval)


if __name__ == "__main__":
    main()
