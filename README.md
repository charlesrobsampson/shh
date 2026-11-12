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

It needs one permission: **Accessibility**, which is how it hides notification banners while focus is on (System Settings, Privacy & Security, Accessibility). It reads the notification database as you, which needs nothing extra on the macOS it was built on.

## First run

```sh
shh init            # writes ~/.config/shh/config.json with every option at its default, explained
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

Changes to the `alert` section show in the **next banner**, with no restart. Everything else in the file is read when the worker starts (`shh worker restart`). Colors are numbers from the [8-bit ANSI palette](https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit), 0 to 255, not hex codes; `shh alert colors` draws the palette with each number.

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
```

`plan.md` and `manifest.md` describe the design, `checklist.md` the work done and still to do, `process.md` how the work is done.
