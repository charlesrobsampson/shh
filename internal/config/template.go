package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Template is a config.json with every option written out at its default and explained, so a person who has just installed shh can see
// what there is to change. Loading it gives exactly the configuration that no file at all gives (a test keeps it that way). Options that
// have no default value (they are off until set) are there as comments to uncomment.
const Template = `// shh configuration. Every option is shown at its default, so this file changes nothing until you edit it. Comments (// and /* */)
// and trailing commas are allowed. A change to the "alert" section shows in the next banner, "retention" applies at the next cleanup (about every
// six hours), "summarizer" when the terminal UI is opened; only "menubar" and "paths" need shh worker restart. "shh config show" prints what is in force, and "shh guide config" explains it all.
{
  "alert": {
    // How banners look. Colors are numbers from the 8-bit ANSI palette, 0-255 (shh alert colors shows it; chart:
    // https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit).
    "position": "top-right",    // top-left, top-center, top-right, bottom-left, bottom-center or bottom-right
    "size": "medium",           // small, medium or large (the banner's width)
    "color": 208,               // the banner's background
    "text_color": 15,           // the text on it (15 is white)
    // "button_color": 238,     // the buttons' background; left out, the buttons look as the system draws them
    // "button_text_color": 15, // the buttons' text
    "duration_secs": 0,         // 0 stays until you dismiss or open it; a number of seconds goes by itself
    "sound": "Glass",           // a macOS system sound (Glass, Hero, Basso, Blow, ...) or "none"

    // The buttons, left to right. A type left out of the list is switched off. The label is what the button says; Preview, Flag and the
    // train button also say something else once pressed.
    "buttons": [
      { "type": "preview", "label": "Preview", "enabled": true, "collapse_label": "Collapse" }, // only on a message that was cut off
      { "type": "goto", "label": "Go to", "enabled": true },                                   // opens the app the notification came from
      { "type": "dismiss", "label": "Dismiss", "enabled": true },
      { "type": "flag", "label": "Flag", "enabled": true, "flagged_label": "Flagged" },        // marks it to review in the Train tab
      { "type": "train", "label": "Not important", "enabled": false, "trained_label": "Noted" } // teaches the model at once; off until you turn it on
    ]

    // Anything above can be changed for one kind of alert or one app. Each sets only what it names; a "buttons" list replaces the whole list.
    // , "calendar": { "color": 39, "sound": "Hero" }
    // , "source": { "Slack": { "sound": "none" }, "Routine": { "color": 33 } }   // "Routine" styles every banner a routine shows
  },

  "menubar": {
    "enabled": true,  // the status item in the menu bar
    "label": "shh"    // the text before the time left, such as "shh 25m · 75%"
  },

  "summarizer": {
    // none, ollama or claude: what writes the short summary of a notification (s in the terminal UI). "ollama" runs on this Mac.
    // "claude" sends the text of the notification to Anthropic's API, so it is off until you choose it: it needs the key in the
    // ANTHROPIC_API_KEY environment variable, or in the Keychain (security add-generic-password -s shh-anthropic -a $USER -w).
    "backend": "none"
    // , "url": "http://localhost:11434"   // the server: this is ollama's default; claude's is https://api.anthropic.com
    // , "model": "llama3.2"               // ollama's default; claude's is claude-haiku-5-5
  },

  // How long shh keeps what it stores. The text of your notifications is private, so nothing is kept forever. Cleanup runs when the
  // worker starts and about every six hours after.
  "retention": {
    "ttl_days": 30,             // a notification is deleted after this many days...
    "keep_untrained_days": 90,  // ...except one still waiting for you to review it (held back, or flagged, and not labelled yet), which stays this long
    // A notification you never labelled can be taken as handled correctly: one that was held back is trained as rightly held back, one
    // that was let through as rightly let through. Flagged ones are left for you. Off until you switch it on. It happens after
    // auto_train_after_days, which cannot be more than ttl_days (a notification must not be deleted before it could be trained on).
    "auto_train": false,
    "auto_train_after_days": 30
  }

  // Where shh keeps its files. These are relative to this folder unless they start with a slash.
  // , "paths": { "dir": "~/.config/shh", "db": "shh.db", "socket": "shh.sock", "pid": "shh.pid" }
}
`

// WriteTemplate puts the documented defaults at path, making the folder if need be, unless there is a file there already, which is
// never touched. It reports whether it wrote one.
func WriteTemplate(path string) (created bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(Template); err != nil {
		f.Close()
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, f.Close()
}
