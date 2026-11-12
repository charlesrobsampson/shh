# shh

`shh` keeps your Mac's notifications quiet while you focus, and lets through only what matters. It reads the notifications macOS gets, scores each one with a small model that learns from your feedback, and hides the banners of the ones that can wait. The important ones are shown by shh itself, in banners you can style, with buttons to open the app, flag it for review or tell shh it was not important.

On top of that, shh runs **routines**: saved sequences of focus sessions (a pomodoro, a deep work block, a whole day's timetable) that can start by themselves on the days and times you choose.

Everything stays on your machine.

## Install

shh is a Go program for macOS. Build it from source:

```sh
task build          # builds ./shh   (or: go build -o shh ./cmd/shh)
mv shh /usr/local/bin/   # or anywhere on your PATH
```

### The embedding model

shh scores notifications with a small model, and its second layer (word meaning, from a bundled 8 MB embedding model) needs the model file. A **release build has it inside**: `task build:release` makes `dist/shh` with the model embedded (it checks the file against the checksum the source pins). A plain `task build` does not carry it, so `shh init` downloads it once into `~/.config/shh/models/` (a network use, checked against the same checksum); `shh doctor` says whether it is there. Without it shh still works on its first two layers, a little less well. When the model arrives the learned weights start from the seed again and **replay what you have labelled**, so your training is not lost.

It needs one permission: **Accessibility**, which is how it hides notification banners while focus is on (System Settings, Privacy & Security, Accessibility). It reads the notification database as you, which needs nothing extra on the macOS it was built on.

## First run

```sh
shh init            # writes ~/.config/shh/config.json (every option at its default, explained), checks the permissions shh needs,
                    # and stores the last 3 days of notifications as unlabeled history for the Train tab (--history 0 skips that)
shh doctor          # checks the whole setup: config, databases, model, permissions, the worker
shh worker start    # starts the background worker (it also starts by itself when a command needs it)
shh 75% 25m writing # a focus session: level, length, and what you are doing
shh                 # opens the terminal UI
shh guide           # the guide to all of it
```

### The configuration file

`shh init` writes `~/.config/shh/config.json` with **every option at its default and explained in comments**, so you can see what there is to change: banner position, size, colors, sound, which buttons banners have, the menu bar item, and more. It never touches a file that is already there.

```sh
shh config path        # where the file is
shh config show        # what is in force: your file over the defaults
shh config defaults    # the documented defaults again, to compare with your edits
shh alert test         # a sample of each banner style
shh alert colors       # the palette banner colors are chosen from
```

Changes to the `alert` section show in the **next banner**, with no restart. `retention` applies at the next cleanup (about every six hours) and `summarizer` the next time the UI opens; only `menubar` and `paths` need `shh worker restart`. Colors are numbers from the [8-bit ANSI palette](https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit), 0 to 255, not hex codes; `shh alert colors` draws the palette with each number.

### Start the worker at login

Scheduled routines (and the menu bar item) need the worker running. shh starts it by itself when a command needs it, but after a restart or a login nothing is running until something starts it. Add this to whatever your Mac runs at login: a Login Item that runs a script, your shell profile, or a launchd agent:

```sh
shh worker start       # starts it in the background (does nothing if it is running)
```

A launchd agent (or anything that wants a process that stays in the foreground) should run `shh daemon` instead. `shh worker status` shows what the worker is watching, `shh worker stop` shuts it down and `shh worker restart` replaces it after you update shh.

## Routines

A routine is a list of steps, each written as the `shh` command that would do it:

```sh
shh routine example    # prints a routines file to start from
shh routine add pomodoro "75% 25m work --alert banner" "off 5m break" -r 4 -f "off 15m long break"
shh routine run pomodoro
```

Routines can run other routines, loop forever, wait for you between steps, end at a time of day (`until 12:00`), look different from your other banners, and **start by themselves**: `"triggers": ["weekdays at 09:00 until 12:00"]`. Every run is kept, with what it held back and any notifications you flagged during it (`shh routine log`).

## Looking at and teaching what shh did

```sh
shh ls --since 1d --status blocked --min-score 40   # what was held back, newest first (read-only; --json for scripts)
shh train 101 102 --yes                              # these should have reached me (--no: rightly held back, --label 0-100: exact)
shh train --undo | --reset | --rollback              # take back a label, go back to the seeded model, or to the last checkpoint
shh train --export | --import                        # labelled notifications to and from private/training.jsonl (it holds their text)
shh purge                                            # delete notifications past their time to live now, asking first (--all: every one)
shh rules review                                     # calendar reminders shh could not read a time from, and how to teach it
shh capture --app teams                              # log raw notifications to private/captures/raw.jsonl, to build rules from
```

## The guide

The guide is the documentation, and it is built into shh:

```sh
shh guide               # opens it in the terminal UI: / searches, n and N go to the next and previous match
shh guide routines-tab  # opens it at a section (shh guide --list names them)
shh guide -s trigger    # opens it at the first match of a search
shh guide --print       # writes it to the terminal instead, for a pager or a file
```

In the terminal UI, `?` shows the same guide.

## Development

```sh
task check     # format check, vet and test: run before every commit
task build     # build ./shh
task build:release   # build dist/shh with the embedding model inside
task model:publish   # publish the model as the models-v1 release asset that shh init downloads
```

`plan.md` and `manifest.md` describe the design, `checklist.md` the work done and still to do, `process.md` how the work is done.
