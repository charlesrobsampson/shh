// Package guide is shh's guide: the keys of the terminal UI, the routines file and the commands, written once for the help screen
// in the UI (shh guide opens it, ? in the UI shows it) and for printing in a terminal (shh guide --print).
package guide

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Topic is one entry of a section: the keys or words it is about, and what they do, in full.
type Topic struct{ Keys, Text string }

// Section is a group of topics under a title. Slug is what names it on the command line.
type Section struct {
	Slug, Title string
	Topics      []Topic
}

// Sections is the guide, in the order it is shown.
var Sections = []Section{
	{"moving", "Moving around", []Topic{
		{"j / k, ↓ / ↑", "Down or up one row. Type a number first to repeat it: 6j goes down six rows, 3k up three. The numbers beside the rows make this easy: every row shows how far it is from the cursor, so the count to type is the number you can see; the cursor's own row shows its place in the list."},
		{"g / G", "The first or last row. With a number, 5G (or 5g) goes to row 5."},
		{"PgUp / PgDn", "A page at a time."},
		{"Tab, l, →", "The next tab. Shift-Tab, h or ← goes back. A number repeats it, so 2l skips one."},
		{"! @ # $", "Jump straight to Focus, Notifications, Train or Routines (Shift-1 to Shift-4). F1 to F4 do the same."},
		{"/", "Filter both lists: type some text and press Enter to see only the notifications whose sender, app, title or message contain it (case does not matter). The filter shows beside the tabs. Esc clears it; an empty filter does too."},
		{"Esc", "Drops a number you have started typing, or closes a message you are reading, or clears the filter."},
	}},
	{"focus", "Focus tab", []Topic{
		{"what you see", "The sessions in force, top first. ▶ marks the one in force; the others are what it falls back to as sessions end. Each shows its level and its time left (or that it runs until stopped), and a note if it has one. Below it is a menu of everything you can do."},
		{"j / k, Enter", "Select a menu row and run it with Enter (a number first moves further, so 3j goes down three rows). Rows that do not apply right now are dimmed, with the reason; Enter on one explains instead of doing anything. Every row also has its own key, shown beside it, which works without selecting it. The line below the page shows, at its right, the equivalent shh command for the selected row, and after you run something it shows the exact command that would have done the same, such as $ shh 75% 25m writing docs. Changing the level and adding time have no command line equivalent yet, so they say TUI only."},
		{"s", "Start a session on top of what is in force. Type a level, a length and a note in any order, the way the shh command takes them: 75% 25m writing docs, or deep 1h, or just 30%. Add then off (or then a level) at the end to choose what it falls back to. Nothing typed starts medium until stopped. If shh is not running, a worker is started for you."},
		{"b", "Set the standing level that applies whenever no session does: a percentage, a tier, or off to clear it."},
		{"f", "Change the level of the session in force, without starting a new one."},
		{"p, Space", "Pause the countdown of a timed session, or start it again. Only the timer stops; the level stays."},
		{"+ / -", "Add or take away five minutes from the time left. A number first repeats it: 3+ adds fifteen, and 3 then Enter on the menu row does the same. Time cannot go below zero."},
		{"x", "Stop the session in force; focus falls back to what is under it."},
		{"X", "Turn focus off completely, baseline included. It asks first: y confirms, n or Esc keeps it."},
	}},
	{"routines-tab", "Routines tab", []Topic{
		{"what you see", "The routine that is running, if any, with the step it is on, how far through it is and what comes next (or the step it is waiting for you to start). Below it are the routines saved in routines.json, each with its steps written as the shh commands that do them. Under No routine is running, a last: line gives the summary of the run that ended, for routines whose summary setting includes ui. Edit that file to change them; the next run uses the new text. shh routine example prints a file to start from."},
		{"j / k, Enter", "Select a routine and start it with Enter (or s). Only one runs at a time."},
		{"n", "Go on: start the step that is waiting for you, or end the running step early and move to the next. Steps that only set the baseline are applied on the way."},
		{"b", "Restart the step before this one (the step that just ended, while waiting)."},
		{"x", "End the routine now. Whatever it had in force is stopped."},
		{"S", "Skip the selected routine's next scheduled start, or take the skip back if it was skipped (see Triggers). A routine with triggers shows a clock line under its steps: when it starts next, then its triggers."},
		{"a, e, c, d", "a makes a new routine, e edits the selected one, c edits a copy of it under a new name, d deletes it (asking first). The same things from a terminal: shh routine add and shh routine remove; the line under the page shows the command that would save what you are editing."},
		{"the editor", "j and k move over the rows, Enter changes one: Name, Description and Repeat take typing; Waiting and Summary go to their next choice. Each step is one shh command, such as 75% 25m work --alert banner or baseline 30% (--ask makes it wait for you before the next). Enter on a step edits it, a adds one, d deletes one (a routine keeps at least one), J and K move it down or up. s saves; only that routine's text in routines.json changes, so your comments and the rest of the file stay as they were. Esc cancels; with unsaved changes it asks for a second Esc."},
		{"editing triggers", "Below the steps are the routine's triggers, one row each. Enter on one (or a to add one) opens the trigger form: Days is a row of checkboxes (h and l pick a day, Space ticks it, w is weekdays, e weekends, a every day), then Start, Stop, Reminder, Ask first, When busy, Grace, Stop alert and Switched on; j and k move between rows, Enter changes one (the times, reminder and grace are typed, the others go to their next choice), s accepts the trigger and Esc drops the changes. The form shows the trigger as the words it will be saved as, and when it would start next. d deletes a trigger from the editor."},
		{"on the Focus tab", "While a routine runs, starting or stopping a session by hand asks first: y ends the routine and does what you asked, n leaves everything as it was. For a new session there are two more answers. c interrupts the routine: your session starts now, the routine's step stops counting down, and the routine continues from there when your session ends (Stop on your session ends it early, and the routine resumes at once). q queues your session: nothing changes now, and it starts when the routine ends, however it ends. u on the Routines tab drops a queued session. The Focus tab also shows the routine's step under the level in force."},
	}},
	{"config", "Configuration (config.json)", []Topic{
		{"shh init", "Run shh init once after installing. It makes the shh folder (~/.config/shh) and writes config.json there with every option at its default and explained in comments, so you can see what there is to change. It does nothing to a config.json that is already there. The file is JSON with comments allowed (// and /* */) and trailing commas."},
		{"shh config", "shh config path prints where config.json is, shh config show prints the configuration in force (your file laid over the defaults), and shh config defaults prints the documented defaults again, to compare with a file you have edited."},
		{"when it applies", "The alert section applies to the next banner, with no restart: the worker looks at the file when it shows one. A mistake in the file leaves the last good settings in use, and the worker's log says so once. Everything else (the menu bar, the summarizer, where files live) is read when the worker starts, so after changing those run shh worker restart."},
		{"alert", "How banners look and which buttons they have: position, size, color, text_color, button_color, button_text_color, duration_secs, sound and the buttons list, with overrides for calendar alerts and for one app (source). See Alerts."},
		{"menubar", "\"menubar\": {\"enabled\": true, \"label\": \"shh\"}: whether the worker shows the status item in the menu bar, and the text before the time left."},
		{"summarizer", "\"summarizer\": {\"backend\": \"none\"}: what writes the short summary of a notification: none, ollama or claude."},
		{"paths", "\"paths\": {\"dir\": ..., \"db\": \"shh.db\", \"socket\": \"shh.sock\", \"pid\": \"shh.pid\"}: where shh keeps its files. They are inside the shh folder unless a path starts with a slash. Rarely changed."},
		{"other files", "Next to config.json: routines.json (see Routines file; shh routine example prints one), rules.json (the rules that decide what always gets through) and shh.db (everything shh has seen and learned). The embedding model, which the classifier uses, is kept in the models folder."},
		{"a problem", "A mistake in config.json is an error that names the file, the line and the key, such as alert.color: 300 is not a color code from 0 to 255. A color written as #rrggbb says that colors are numbers now."},
	}},
	{"alerts", "Alerts: banner color, position, size and sound", []Topic{
		{"live changes", "A change to the alert section of config.json shows in the next banner: the worker looks at the file when it shows one, so it does not need restarting. If the file has a mistake, the last good settings stay in use and the worker's log says so once. Everything else in config.json (the menu bar, where files live) is read when the worker starts, so those need shh worker restart."},
		{"where", "In config.json (~/.config/shh), the alert section sets how banners look for everything: \"alert\": {\"position\": \"top-right\", \"size\": \"medium\", \"color\": 208, \"text_color\": 15, \"button_color\": 238, \"button_text_color\": 15, \"duration_secs\": 0, \"sound\": \"Glass\"}. alert.calendar changes those for calendar alerts and alert.source.<app> for one app's (\"Teams\", \"Slack\", or \"Routine\" for every routine banner); each sets only what it names. shh alert test shows a sample of each."},
		{"position", "top-left, top-center, top-right, bottom-left, bottom-center or bottom-right. Banners shown together stack along the edge."},
		{"size", "small, medium or large: the banner's width."},
		{"color", "The banner's background: a number from 0 to 255 from the 8-bit ANSI palette, not a hex code. 0-15 are the standard and bright colors, 16-231 a cube of colors, 232-255 greys. Chart: https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit. shh alert colors draws the palette in your terminal with each number (shh alert colors 208 shows one, and the #rrggbb it is drawn as). The default is 208, an orange."},
		{"text_color", "The color of the text on the banner (its title, message and the app name above them, which is a little fainter), a number from the same palette. The default is 15, white, which suits a dark background; pick 0 (black) or another dark color for a light one."},
		{"the Not important button", "A button that teaches the model at once that this alert should not have come, the same answer as n on the Notifications tab (u undoes it). Off by default: turn it on by listing it in alert.buttons, such as {\"type\": \"train\", \"label\": \"Not important\", \"trained_label\": \"Noted\"}. It appears only on alerts about a stored notification, says Noted after a press and cannot be pressed twice. It only covers the alerts that came through: for a notification shh held back that should have reached you, use the held back view (v) in the UI."},
		{"button layout", "The Dismiss button sits in the top right corner of the banner, as wide as its words. The other buttons are in a row along the bottom, left to right in the order of the alert.buttons list, each as wide as its words; when they do not all fit across a narrow banner they go on to a second row, and the banner grows to make room. A button that says something else once pressed (Collapse, Flagged, Noted) is given room for the longer words."},
		{"button_color", "The background of the banner's buttons (Preview, Go to, Dismiss, Flag, and the Start next, Skip and Start now buttons of routines), a number from the same palette. Left out, the buttons look as the system draws them. With only a button color, the button text takes the banner's text color."},
		{"button_text_color", "The color of the text on the buttons, from the same palette. Left out, the system's. A button whose colors are set is drawn flat (the system's raised look cannot be recolored), so with only a text color the buttons get a faint white face."},
		{"duration_secs", "How long a banner stays: 0 (the default) until you dismiss or open it, or a number of seconds."},
		{"sound", "The name of a macOS system sound (Glass, Hero, Basso, Blow, ...), or none. It plays when the banner appears, and is the sound a sound alert plays."},
		{"in a routine", "A routine can change any of these for its own banners, over the config's: \"style\": {\"color\": 33, \"text_color\": 15, \"size\": \"large\", \"position\": \"bottom-left\", \"duration_secs\": 10, \"sound\": \"Hero\"} at the top of the routine (it applies to its step-end banners, its summary, the cut-off at a trigger's until, and the reminder and ask banners of its triggers). From a terminal: shh routine add ... --style=\"--color 33 --size large\". In the TUI editor, the Banner style row."},
		{"in a step", "A step can change them again for the banner that announces its own end, over its routine's: flags on the step, such as 75% 25m work --alert banner --color 196 --text-color 15 --button-color 238 --button-text-color 15 --sound Basso --position top-left --size large --banner-for 10s (--banner-for 0 stays until dismissed). In the object form of a step, a style object like the routine's."},
		{"order", "What is set last wins: the config's base, then the config's kind and app overrides, then the routine's style, then the step's. A routine that another one runs keeps its own look for its steps (under their own flags). Which buttons a banner has, and what they are called, is only set in config.json (alert.buttons); how they look is the style."},
	}},
	{"run-log", "Run log: what a run did and what to review", []Topic{
		{"what is kept", "Every run of a routine is kept: when it started and ended, how it ended (finished, stopped, cut off at its end time), how many steps ran, how many notifications were held back and let through, and how many notifications were flagged for review while it ran. Nothing needs switching on."},
		{"flagged", "A notification is flagged with the Flag button on its banner, or f in the UI. A run counts the notifications that arrived while it ran and are flagged when it ends. Reviewing a flagged notification (training it on the Train tab) takes its flag away, so a run shows both how many were flagged and how many still wait."},
		{"shh routine log", "On the command line: shh routine log, shh routine log pomodoro, shh routine log --run 12 (that run in full, with the notifications still to review). Nothing is hidden from you here, so it shows the sender and subject of each."},
		{"H on the Routines tab", "The history: every run, newest first, and under the selected one what is flagged and waiting. j and k pick a run, R goes to the Train tab (flagged ones lead the list), H or Esc goes back. It reads the history directly, so it works with no worker running."},
		{"telling you", "A routine's log setting says where to be told when a run that ended had flagged notifications waiting: banner (with a Review button), menu (an item in the menu bar menu for an hour) and ui (a line on the Routines tab). The finished-run summary, wherever it is shown, also says how many were flagged. The default is not to tell you: the log is there when you want it."},
		{"the end of a run", "The count is made when the run ends, however it ends: finished, stopped by you, cut off at a trigger's end time, or ended because the routine was removed."},
	}},
	{"routine-files", "Routines file (routines.json)", []Topic{
		{"where", "routines.json in the shh folder (~/.config/shh). It is JSON with comments allowed (// and /* */) and trailing commas. shh routine example prints a complete example to start from. shh changes only the routine you save or delete when it edits the file, so your comments, order and layout elsewhere stay as you wrote them."},
		{"shape", "{\"routines\": {\"pomodoro\": {...}, \"deep work\": {...}}}. A routine's name is its key, so a name can be there only once (capitals do not make a different name). Names start with a letter or digit and use letters, digits, spaces, dots, dashes and underscores, since they are typed on the command line."},
		{"steps", "A list of steps, run in order. Each is written as the shh command that does it (see Writing steps). A routine needs at least one."},
		{"repeat", "How many times the steps run, 1 to 99 (the default is 1), or the word \"forever\" to go round again and again until you stop it. A routine that repeats forever has no finish steps."},
		{"finish", "Steps that run once after the last round, such as a long break. Written like steps."},
		{"description", "A line saying what it is for, shown in the list."},
		{"triggers", "Optional ways for the routine to start by itself, such as \"weekdays at 09:00 until 12:00\". See Triggers."},
		{"log", "Optional: where to say that notifications were flagged while a run was going, so they can be reviewed: any of banner (when the run ends, a banner says how many and has a Review button that opens the UI on the Train tab), menu (a Review item at the top of the menu bar menu for an hour) and ui (a line on the Routines tab, with R to go to the Train tab). None by default. Every run is kept either way: see Run log."},
		{"style", "Optional: how this routine's banners look, over the config's (color, text_color, button_color, button_text_color, position, size, sound, duration_secs). See Alerts."},
		{"waiting", "What focus does while the routine waits for you to start the next step (a step that asks): stay (the default: the level of the step that ended stays in force, so walking away does not let notifications through), next (the next step's level, with its countdown held until you start it) or baseline (fall back to the baseline). A step that ends at a time of day has no countdown to hold, so next behaves like stay for it."},
		{"summary", "Where to report a finished run, any of: banner (a banner when it ends), menu (a line at the top of the menu bar menu for an hour) and ui (a last: line on the Routines tab). The default is banner and ui."},
		{"mistakes", "A mistake anywhere in the file stops every routine from loading, with a message that names the file, the line and the key, such as routines.pomodoro.steps[1]: a half-read file could start the wrong thing. Fix it and the next command works."},
	}},
	{"steps", "Writing steps", []Topic{
		{"a step", "A step is a shh command: a level, a length and a name, in any order, the way shh focus takes them. 75% 25m work is a 25 minute session at 75% called work. A leading shh or shh focus is allowed, so a line can be pasted from a terminal. It is only read, never run through a shell."},
		{"level", "A percentage (75%) or a tier: off (0, a break with nothing held back), light (35%), medium (65%, the default), deep (90%) or lockdown (99%). A tier word is a level when it is alone or beside another setting: deep 25m is a level and a length, deep work is a name."},
		{"length", "25m, 1h30m or 90s, or until 11:00 to last until a time of day. The time is worked out when the step starts: a step whose time has already gone by is skipped, so a run that starts late joins in where the clock is, and a run with nothing left ends. A step has a length or an end time, not both."},
		{"name, -m", "The words that are not a level or a length are the step's name, shown as work (2/4) when the routine repeats, and its note. -m gives a different note: 75% 25m work -m \"write the report\". A name that would look like a setting is given with -m."},
		{"banner style", "--color 196, --text-color 15, --button-color 238, --button-text-color 15, --position top-left, --size large, --sound Hero and --banner-for 10s change how the banner that announces this step's end looks, over the routine's style and the config's. Colors are numbers 0-255 from the 8-bit ANSI palette (see Alerts; shh alert colors shows them). Not for run steps."},
		{"--alert", "--alert banner (or sound, or none, the default) announces the end of the step. A banner says what comes next and carries a Start next button when the step asks, or a Skip button when the next step has already begun."},
		{"--ask", "Waits for you before the next step begins (shh routine next, the Start next button, or n on the Routines tab). Only a routine uses it. What focus does meanwhile is the routine's waiting setting."},
		{"baseline", "baseline 30% sets the standing level when the run reaches that step, and baseline off clears it. It takes no time and never waits. It still applies after the last session, and when the session before it is skipped."},
		{"run", "run pomodoro puts another routine's steps in this one's place: its repeats and its finish steps included, named pomodoro › work (2/4). --times 2 does it twice. --ask and --alert on the call apply when the called routine ends. A name with spaces needs no quotes. The called routine's own waiting and summary settings do not apply: the routine that was started decides."},
		{"run limits", "A routine cannot call itself, directly or through others: a cycle such as a → b → a is refused, with the path, routines may not be nested more than 5 deep, and a routine that repeats forever cannot be called, since nothing after it could run. A call to a name that does not exist is refused. These are checked whenever the file is read, and when you try to delete a routine another one calls."},
		{"objects", "A step can also be an object: {\"name\": \"work\", \"level\": \"75%\", \"length\": \"25m\", \"note\": \"...\", \"alert\": \"banner\", \"advance\": \"ask\"} (until in place of length), or {\"baseline\": \"30%\"}. Calls are written as commands."},
		{"going round", "repeat: \"forever\" in the routine is how to loop. A routine that is only steps with a time of day (until) works as a day's timetable."},
	}},
	{"triggers", "Triggers: routines that start by themselves", []Topic{
		{"what", "A routine can have triggers, each a way for it to start without you: on some days of the week at a time of day. A routine can have any number, so the same steps can start Monday to Thursday at 9 and Friday at 10, and a different set of steps has its own triggers. Different times mean different triggers (or another routine). shh routine list shows them and when each routine starts next."},
		{"writing one", "In the routine, \"triggers\": [\"weekdays except fri at 09:00 until 12:00 --reminder 5m\", \"fri at 10:00 --ask\"]. A trigger is written as words, like a step: the days first, optionally except and the days to leave out, then at and the start time, then optionally until and the stop time, then flags. From a terminal: shh routine add morning \"...\" -t \"weekdays at 09:00\" (repeat -t for more)."},
		{"days", "Names (mon, tue, wed, thu, fri, sat, sun; the full names work too), ranges (mon-fri, and fri-mon wraps over the weekend), weekdays, weekends and daily, separated by commas or spaces. Leave the days out for daily. except takes days to leave out: weekdays except fri."},
		{"at, until", "at 09:00 is the start (24 hour, local time). until 12:00 is a hard stop on the same day: whatever the routine is doing at that time is cut off, even in the middle of a step, and the run ends (a session you queued to start when the routine ends starts then). It does not repeat the routine: if the steps finish sooner, the run just ends, so use repeat: \"forever\" in the routine to fill the time. A start that is already past the stop time is not made."},
		{"--reminder 5m", "A banner that long before the start says the routine is coming, with a Skip this one button. It is not shown for a start you have skipped."},
		{"--ask", "Instead of starting by itself, show a banner: Start pomodoro? with Start now and Skip buttons. The start is not made until you press Start now (which works for a while after it was due, as long as the stop time has not passed). Dismissing the banner leaves it not started; shh routine run still starts it by hand."},
		{"--when-busy", "What to do when the time comes and something is running (a routine, or a focus session of yours; a baseline alone does not count): ask (the default) shows the Start now / Skip banner and says what is running, skip leaves what is running alone and does not start, start starts anyway, ending a running routine and leaving a session of yours underneath."},
		{"--grace 15m", "How late a start can still be made, 15 minutes unless this says otherwise (0 for none, up to 6 hours). If the worker was not running, or the Mac was asleep, at the start time, a start later than this is skipped; it is never caught up the next day. A routine with until steps that starts late joins in where the clock is."},
		{"--until-alert", "How the cut-off at until is announced: none (the default), sound or banner (a banner that says the routine stopped and what it did)."},
		{"--disabled", "Keeps the trigger in the file without using it, for a holiday. A routine whose triggers are all disabled does not start by itself, and does not keep the worker running."},
		{"objects", "A trigger can also be an object: {\"days\": \"weekdays\", \"except\": [\"fri\"], \"at\": \"09:00\", \"until\": \"12:00\", \"until_alert\": \"banner\", \"reminder\": \"5m\", \"ask\": true, \"when_busy\": \"skip\", \"grace\": \"10m\", \"enabled\": false}. Saving a routine from shh writes its triggers as words, with the except taken out of the day list."},
		{"skipping", "shh routine skip morning leaves out the next scheduled start (whichever trigger it comes from), once; shh routine skip --undo morning takes that back. shh routine status shows what starts next, and marks a skipped one."},
		{"the worker", "Triggers fire only while the shh worker is running, and the worker stays running for as long as any routine has a trigger switched on. After a restart or a login nothing is running until something starts it, so start the worker at login: add shh worker start to whatever your Mac runs at login (a Login Item that runs a script, your shell profile, or a launchd agent that runs shh daemon, which stays in the foreground). shh worker status shows what it is watching, and shh routine list says so when triggers are waiting for a worker that is not running."},
		{"what is remembered", "What was done about each start (started, asked, skipped, missed) is kept for a week, so a worker that restarts does not start the same one twice, a skip holds, and a stopped run is not started again by the trigger that started it."},
	}},
	{"routine-commands", "Routine commands (shh routine ...)", []Topic{
		{"list", "shh routine list shows each routine with its steps written as commands, how long it takes (or at least how long, if some steps end at a time of day, or until stopped), and any triggers."},
		{"example", "shh routine example prints a complete routines file to start from."},
		{"add", "shh routine add <name> \"<step>\" \"<step>\" ... saves a routine, each step in quotes. -d \"text\" describes it, -r 4 (or -r forever) repeats it, -f \"<step>\" adds a finish step (repeat the flag), -w stay|next|baseline sets waiting, --style=\"--color 33 --size large\" sets the routine's banner style (use the = form, since the value starts with dashes), --summary banner,menu,ui says where to report it, -t \"<trigger>\" adds a trigger (repeat the flag), --replace overwrites a routine of the same name. A step or a flag that is wrong says which one."},
		{"remove", "shh routine remove <name> (or rm) deletes a routine, asking first on a terminal; -y skips the question. Deleting one that another routine calls is refused."},
		{"run", "shh routine run <name> starts one (a worker is started if none is running). Only one runs at a time."},
		{"next, back", "shh routine next starts the step a waiting routine is waiting on, or ends the running step early and goes on to the next (it ignores a step's ask and announces nothing). shh routine back restarts the step before the one in focus (the step that just ended, while waiting)."},
		{"stop", "shh routine stop ends the run, closing what it had in force and recording a summary."},
		{"status", "shh routine status shows the run in progress, or the last one that ended and what it did. shh status shows a Routine: line too."},
		{"log", "shh routine log lists the runs of routines, newest first: when, which routine, how it ended, how long, how many steps, what was held back and let through, and how many notifications were flagged during it and still wait for review. shh routine log <name> for one routine, -n 30 for more runs, --run 12 for one run in full with its flagged notifications. It reads the history directly, so it works with no worker running. See Run log."},
		{"ui", "shh ui opens the terminal UI (so does bare shh on a terminal), and shh ui --tab train opens it on a tab: focus, notifications, train or routines."},
		{"unqueue", "shh routine unqueue drops a session that was queued to start when the routine ends."},
		{"skip", "shh routine skip <name> leaves out the routine's next scheduled start; --undo takes that back (see Triggers)."},
		{"worker", "shh worker start starts the background worker (nothing happens if it is running), shh worker stop shuts it down, shh worker restart replaces it, for picking up a new build of shh, and shh worker status says whether it is running, since when, and what it is keeping going (focus, a routine, triggers). Stopping asks first on a terminal while focus or a routine is on, since banners would come through while it is down (they are saved, and carry on when a worker starts again); --force skips the question. shh starts a worker by itself when a command needs one, and it leaves by itself when nothing is on and no trigger is switched on."},
		{"daemon", "shh daemon runs the worker in the foreground, for a login item or launchd agent, which want a process that does not detach. shh worker start is the same thing in the background."},
		{"shh focus", "While a routine runs, shh focus asks what to do about it (see During a run). Without a terminal, give the answer as a flag: --end-routine, --continue-routine or --after-routine. shh stop takes --end-routine."},
	}},
	{"during-a-run", "During a routine run", []Topic{
		{"steps", "Each step starts as an ordinary timed focus session, so pause, add and take away time, the menu bar and shh status all work on it. When a step ends the next one starts at that moment, so every step gets its full length."},
		{"asking", "A step with --ask stops after it ends and waits for you. n on the Routines tab, shh routine next, the Start next button on its banner, or Start in the menu bar menu starts the next step."},
		{"the menu bar", "The menu bar title reads shh pomodoro · work (2/4) · 14m (a pause sign while paused, and ? while it waits for you). Its menu has Skip to the next step (or Start the waiting step), Back a step and Stop routine, in place of the Stop and Turn off items, which a run refuses."},
		{"your own session", "Starting a session by hand while a routine runs asks what to do with the routine: end it and start your session; interrupt it (your session starts now, the routine's step stops counting down, and the routine continues from there when your session ends); queue your session (nothing changes now, and it starts when the routine ends, however it ends); or leave it as it is."},
		{"interrupting", "While your session is on top, stop (x, shh stop) ends it and the routine resumes at once. Next and back end it and move on. Stopping the routine leaves your session running by itself. If the worker restarts, the run picks up where it was."},
		{"queueing", "One session can be queued; queueing another replaces it. It is checked when you queue it, so it cannot fail later. The Routines tab, shh routine status and the menu show what is queued; u or shh routine unqueue drops it."},
		{"stopping", "Turning focus off completely, or stopping a session, while a routine runs asks first, since the routine would otherwise just start the next step. Ending the routine ends the session it had in force."},
		{"finishing", "A run ends after its last step, or when you stop it, or if its routine disappears from the file. The summary says how many steps ran, how long it took, and how many notifications were held back and let through. An edited routine is followed as edited, with the run staying at its step."},
		{"the worker", "The worker stays running while a routine is going, even with nothing in force, since it is what moves the routine on."},
	}},
	{"reading", "Reading", []Topic{
		{"v", "On the Notifications and Train tabs, goes through three views: everything, only what shh held back, and only what it let through; the tab bar says which. Held back is the one to look at for a notification that should have reached you: it never made a banner, so this is the place to say so. In that view n means it should have reached me (it teaches the model to alert on that kind of message) and y means holding it back was right. Esc shows everything again."},
		{"e", "Read the whole message across the screen, with the list out of the way. j and k scroll it (numbers work here too), e or Esc puts it back. Reading this way does not mark the notification as read."},
		{"Enter, o", "Open the app the notification came from and mark it read. If the app cannot be opened, it is not marked."},
		{"d, x", "Dismiss it from the Notifications list. It stays in the Train list: dismissing is not training."},
		{"f", "Flag it to look at again later. Flagged rows lead the Train tab, so a notification you noticed was wrong is easy to find. Press f again to clear the flag. The Flag button on an alert banner does the same thing; labelling a notification clears its flag."},
	}},
	{"training", "Training (Notifications and Train tabs)", []Topic{
		{"y / n", "Answer \"did shh do the right thing?\" The row shows what shh did: ✕ blocked means it held the notification back, no mark means it alerted you. y says the call was right (a blocked one should stay blocked, an alerted one should have alerted); n says it was wrong. A right call close to the line also teaches the model something, by pushing the score further from it. On the Train tab the question \"Was this correct?\" sits under each notification's score and says what shh did. The row then leaves the Train list, and when you label it from Notifications it is archived from there too."},
		{"t", "Type an exact score instead, from 0 to 100 or light, medium, deep or lockdown, and press Enter. Esc cancels. While the box is open, digits and letters are typed into it."},
		{"u", "Undo the last training exactly: the model goes back to how it was and the row returns to the list."},
		{"order", "The Train list shows flagged rows first, then those whose score is closest to a common threshold (35, 65, 90 or 99), because a label there changes what gets through the most."},
	}},
	{"marks", "What the marks mean", []Topic{
		{">", "The cursor. Its row is also highlighted."},
		{"•", "Not opened yet."},
		{"✕ blocked", "shh held it back and it never reached you. A row with nothing here was alerted."},
		{"⚑", "Flagged for review."},
	}},
	{"typing", "Typing in a box", []Topic{
		{"← / →", "Move the cursor through what you have typed, so a typo can be fixed without retyping the rest. Home and End (or Ctrl-A and Ctrl-E) go to the start or the end; Alt-← and Alt-→ jump a word."},
		{"Backspace, Del", "Delete the character before or under the cursor. Ctrl-W deletes the word before it, Ctrl-U everything before it, Ctrl-K everything after it."},
		{"Enter, Esc", "Enter accepts the text; if it cannot be used, the box stays open with the reason under it. Esc cancels. A text longer than the box scrolls to keep the cursor in view."},
	}},
	{"other", "Other", []Topic{
		{"?", "This help: / searches it (n and N go to the next and previous match), j and k scroll, Esc closes it. From a terminal, shh guide opens it directly, and shh guide routines jumps to a section; shh guide --list names them."},
		{"q, Ctrl-C", "Quit."},
	}},
}

// Find returns the section a word names: its slug, or the start of its slug or title, ignoring case. It is false when nothing matches
// or when more than one section does and none is the exact one.
func Find(word string) (Section, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return Section{}, false
	}
	var found []Section
	for _, s := range Sections {
		if s.Slug == word {
			return s, true
		}
		if strings.HasPrefix(s.Slug, word) || strings.HasPrefix(strings.ToLower(s.Title), word) {
			found = append(found, s)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Section{}, false
}

// Listing is the sections one to a line: "routine-files  Routines file (routines.json)".
func Listing() string {
	var b strings.Builder
	for _, s := range Sections {
		fmt.Fprintf(&b, "%-18s %s\n", s.Slug, s.Title)
	}
	return b.String()
}

// LineKind says what a laid-out line is, so a screen can style it.
type LineKind int

const (
	LineBlank   LineKind = iota
	LineHeading          // the guide's own title
	LineSection
	LineRow // a topic: its keys beside the first line of its text, then the rest of the text
)

// Line is one line of the guide laid out for a width.
type Line struct {
	Kind LineKind
	// Slug is the section the line is in.
	Slug string
	// Keys is the topic's keys on its first line, and empty on the lines that carry on its text.
	Keys string
	Text string
	// Topic numbers the topic within the layout (from 0), the same on every line of one topic; -1 for lines that are not part of one.
	Topic int
	// First says this is the topic's first line, which is where a search lands.
	First bool
}

// KeyWidth is the width of the column the keys are in.
const KeyWidth = 14

// Layout lays the sections out for a screen width, wrapping each topic's text beside its keys.
func Layout(sections []Section, width int) []Line {
	textWidth := max(20, width-KeyWidth-4)
	lines := []Line{{Kind: LineBlank, Topic: -1}, {Kind: LineHeading, Text: "shh guide", Topic: -1}, {Kind: LineBlank, Topic: -1}}
	topic := 0
	for _, s := range sections {
		lines = append(lines, Line{Kind: LineSection, Slug: s.Slug, Text: s.Title, Topic: -1})
		for _, t := range s.Topics {
			keys := runewidth.Truncate(t.Keys, KeyWidth-1, "…")
			for i, part := range Wrap(t.Text, textWidth) {
				l := Line{Kind: LineRow, Slug: s.Slug, Text: part, Topic: topic, First: i == 0}
				if i == 0 {
					l.Keys = keys
				}
				lines = append(lines, l)
			}
			topic++
		}
		lines = append(lines, Line{Kind: LineBlank, Slug: s.Slug, Topic: -1})
	}
	return lines
}

// SectionStart is the index of the first line of a section in a layout, or -1.
func SectionStart(lines []Line, slug string) int {
	for i, l := range lines {
		if l.Kind == LineSection && l.Slug == slug {
			return i
		}
	}
	return -1
}

// Wrap breaks text into lines of at most width cells at spaces. A word longer than the width is kept whole on a line of its own.
func Wrap(text string, width int) []string {
	var lines []string
	var cur string
	for _, word := range strings.Fields(text) {
		switch {
		case cur == "":
			cur = word
		case runewidth.StringWidth(cur)+1+runewidth.StringWidth(word) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// Pad writes a line's keys column and text as the guide shows them, without styling.
func (l Line) Pad() string {
	switch l.Kind {
	case LineHeading:
		return " " + l.Text
	case LineSection:
		return " " + l.Text
	case LineRow:
		return "  " + fmt.Sprintf("%-*s", KeyWidth, l.Keys) + " " + l.Text
	}
	return ""
}

// ---- searching ----

// Query is a search text. It ignores case unless it has a capital letter in it (as vim's smartcase does).
type Query struct {
	text  string
	exact bool
}

// NewQuery makes a query of the text; an empty text matches nothing.
func NewQuery(text string) Query {
	exact := false
	for _, r := range text {
		if unicode.IsUpper(r) {
			exact = true
		}
	}
	if !exact {
		text = strings.ToLower(text)
	}
	return Query{text: text, exact: exact}
}

// Text is what was searched for.
func (q Query) Text() string { return q.text }

// Empty says there is nothing to find.
func (q Query) Empty() bool { return q.text == "" }

func (q Query) fold(s string) string {
	if q.exact {
		return s
	}
	return strings.ToLower(s)
}

// Has says whether s contains the query.
func (q Query) Has(s string) bool { return !q.Empty() && strings.Contains(q.fold(s), q.text) }

// Spans cuts s into pieces that alternate between text that is not a match and text that is, starting with one that is not (which
// may be empty): an odd index is a match.
func (q Query) Spans(s string) []string {
	if q.Empty() {
		return []string{s}
	}
	folded := q.fold(s)
	// strings.ToLower can change a string's byte length for a few characters; then there is no safe way to cut, and nothing is marked.
	if len(folded) != len(s) {
		return []string{s}
	}
	var out []string
	rest := 0
	for {
		at := strings.Index(folded[rest:], q.text)
		if at < 0 {
			break
		}
		out = append(out, s[rest:rest+at], s[rest+at:rest+at+len(q.text)])
		rest += at + len(q.text)
	}
	return append(out, s[rest:])
}

// Hits are the indexes of the lines where a topic (or a section title) that contains the query starts, in order. A topic is searched
// whole, keys and text, so a phrase that wraps over two lines is still found.
func Hits(sections []Section, lines []Line, q Query) []int {
	if q.Empty() {
		return nil
	}
	whole := map[int]string{} // the topic's keys and text
	topic := 0
	titles := map[string]string{}
	for _, s := range sections {
		titles[s.Slug] = s.Title
		for _, t := range s.Topics {
			whole[topic] = t.Keys + " " + t.Text
			topic++
		}
	}
	var hits []int
	for i, l := range lines {
		switch {
		case l.Kind == LineSection && q.Has(titles[l.Slug]):
			hits = append(hits, i)
		case l.Kind == LineRow && l.First && q.Has(whole[l.Topic]):
			hits = append(hits, i)
		}
	}
	return hits
}

// Plain writes sections as text for a terminal: each title underlined, then its topics. With a query only the topics that contain it
// are written (a section whose title matches is written whole).
func Plain(sections []Section, width int, q Query) string {
	textWidth := max(20, width-KeyWidth-4)
	var b strings.Builder
	for _, s := range sections {
		wholeSection := q.Empty() || q.Has(s.Title)
		var topics []Topic
		for _, t := range s.Topics {
			if wholeSection || q.Has(t.Keys+" "+t.Text) {
				topics = append(topics, t)
			}
		}
		if len(topics) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n", s.Title, strings.Repeat("=", runewidth.StringWidth(s.Title)))
		for _, t := range topics {
			keys := runewidth.Truncate(t.Keys, KeyWidth-1, "…")
			for i, part := range Wrap(t.Text, textWidth) {
				prefix := strings.Repeat(" ", KeyWidth)
				if i == 0 {
					prefix = fmt.Sprintf("%-*s", KeyWidth, keys)
				}
				fmt.Fprintf(&b, "  %s %s\n", prefix, part)
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
