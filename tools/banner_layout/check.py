#!/usr/bin/env python3
"""Lays out real banners headlessly and checks where the buttons and text land.

It runs the actual banner script (internal/alert/overlay.js) under osascript with the parts that put a window on the screen taken out
(the window is made but never ordered front, the app never runs, no sound), then reads the frames of the title, the body and every button.
Nothing appears on the screen. It needs macOS. Run it with: task banner:layout
"""
import json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "..", "..", "internal", "alert", "overlay.js")


def spec(width, rows, buttons, lines=1, header="Teams", **extra):
    height = 24 + 20 + 4 + 16 * lines + (14 if header else 0) + rows * 32
    d = dict(header=header, title="Ana", body="a message", bundle="", color="#d7ff00", textColor="#000000", width=width, height=height, position="top-left",
             indent=0, offset=0, duration=0, sound="none", buttons=buttons, dismissOnClick=False, previewHeight=0, buttonRows=rows, managed=False, parent=0)
    d.update(extra)
    return json.dumps(d)


def layout(s):
    env = dict(os.environ, HARNESS_SPEC=s, HARNESS_SCRIPT=os.path.abspath(SCRIPT))
    r = subprocess.run(["osascript", "-l", "JavaScript", os.path.join(HERE, "harness.js")], env=env, capture_output=True, text=True, timeout=60)
    if not r.stdout.strip():
        raise SystemExit("the banner script did not lay out: " + r.stderr.strip())
    return json.loads(r.stdout.strip())


STD = [dict(type="preview", label="Preview", collapseLabel="Collapse"), dict(type="goto", label="Go to"), dict(type="dismiss", label="Dismiss"),
       dict(type="flag", label="Flag", flaggedLabel="Flagged"), dict(type="train", label="Not important", trainedLabel="Noted")]
ACTIONS = [dict(type="dismiss", label="Dismiss"), dict(type="action", label="Start now", id="a"), dict(type="action", label="Skip this one", id="b"),
           dict(type="action", label="Review notifications", id="c")]
PAINT = dict(buttonColor="#005fd7", buttonTextColor="#ffffff")

# The rows in each scenario are what the Go side reserves (alert.ButtonRows; internal/alert's tests pin the same numbers for these button
# words), and the checks below are about whether what the script does fits what was reserved.
SCENARIOS = [
    ("medium, all five buttons", spec(360, 1, STD)),
    ("small, all five buttons", spec(280, 2, STD)),
    ("large, all five buttons", spec(460, 1, STD)),
    ("medium, painted buttons", spec(360, 1, STD, **PAINT)),
    ("small, painted buttons", spec(280, 2, STD, **PAINT)),
    ("only dismiss", spec(360, 0, [dict(type="dismiss", label="Dismiss")])),
    ("no app name", spec(360, 1, STD, header="")),
    ("long action buttons, small", spec(280, 2, ACTIONS)),
    ("long action buttons, medium", spec(360, 1, ACTIONS)),
    ("a tall body", spec(360, 1, STD, lines=4)),
    ("a wide dismiss label", spec(360, 1, [dict(type="dismiss", label="Close this notification for now"), dict(type="goto", label="Go to")])),
]


def overlaps(a, b):
    return a[0] < b[0] + b[2] and b[0] < a[0] + a[2] and a[1] < b[1] + b[3] and b[1] < a[1] + a[3]


failures = 0
for name, s in SCENARIOS:
    d = layout(s)
    width, height = d["banner"]
    problems = []
    bottom = [b for b in d["buttons"] if b["type"] != "dismiss"]
    dismiss = [b for b in d["buttons"] if b["type"] == "dismiss"]
    for b in d["buttons"]:
        x, y, w, h = b["frame"]
        if x < 0 or x + w > width + 0.5 or y < 0 or y + h > height + 0.5:
            problems.append(f"{b['label']!r} sticks out of the banner: {b['frame']} in {width}x{height}")
        if b["ink"]:
            ix, iy, iw, ih = b["ink"]
            if ix < x - 0.5 or ix + iw > x + w + 0.5:
                problems.append(f"the words of {b['label']!r} stick out of their button: ink {b['ink']} button {b['frame']}")
    for i, a in enumerate(d["buttons"]):
        for c in d["buttons"][i + 1:]:
            if overlaps(a["frame"], c["frame"]):
                problems.append(f"{a['label']!r} and {c['label']!r} overlap: {a['frame']} {c['frame']}")
    for dm in dismiss:
        x, y, w, h = dm["frame"]
        if abs((x + w) - (width - 16)) > 1 or y + h > height - 8:
            problems.append(f"dismiss is not in the top right corner: {dm['frame']} in {width}x{height}")
        tx, ty, tw, th = d["title"]
        if overlaps(d["title"], dm["frame"]):
            problems.append(f"the title runs under the dismiss button: {d['title']} {dm['frame']}")
        if d["header"] and overlaps(d["header"], dm["frame"]):
            problems.append(f"the app name runs under the dismiss button: {d['header']} {dm['frame']}")
    rows = sorted({b["frame"][1] for b in bottom})
    reserved = json.loads(s)["buttonRows"]
    if len(rows) > reserved:
        problems.append(f"{len(rows)} rows used but {reserved} reserved: {rows}")
    if bottom:
        top_of_buttons = max(b["frame"][1] + b["frame"][3] for b in bottom)
        if top_of_buttons > d["body"][1] + 0.5:
            problems.append(f"the buttons reach into the message: buttons to {top_of_buttons}, message from {d['body'][1]}")
    status = "ok  " if not problems else "FAIL"
    print(f"{status} {name}: {len(bottom)} buttons in {len(rows)} of {reserved} reserved rows" + (", dismiss in the corner" if dismiss else ""))
    for p in problems:
        print("       " + p)
    failures += bool(problems)
sys.exit(1 if failures else 0)
