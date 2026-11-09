package ingest

import (
	"encoding/json"
	"strings"

	"howett.net/plist"
)

// Thread is the kind of conversation a chat app's notification belongs to, read from the app's own metadata rather than guessed from
// the text. Empty means unknown: the app does not say, or it said something not recognised.
type Thread string

const (
	ThreadUnknown Thread = ""
	ThreadDirect  Thread = "direct"  // a one-to-one chat
	ThreadGroup   Thread = "group"   // a chat with several people
	ThreadMeeting Thread = "meeting" // the chat attached to a meeting
	ThreadChannel Thread = "channel" // a channel in a team
)

// threadOf reads the conversation kind out of a notification's user info (the "usda" field). Microsoft Teams puts an
// NSKeyedArchiver archive there whose top-level dictionary names the conversation it came from, for example
// "19:<id>@unq.gbl.spaces" for a one-to-one chat. Only that id is looked at, and only its shape is kept: nothing from the archive
// is stored.
func threadOf(userInfo []byte) Thread {
	values := archivedStrings(userInfo)
	candidates := []string{values["default_id"]}
	for _, key := range []string{"textInputsend_options", "default_options"} {
		var options struct {
			ConvID string `json:"convId"`
		}
		if json.Unmarshal([]byte(values[key]), &options) == nil {
			candidates = append(candidates, options.ConvID)
		}
	}
	for _, id := range candidates {
		if t := threadOfConversation(id); t != ThreadUnknown {
			return t
		}
	}
	return ThreadUnknown
}

// threadOfConversation classifies a Teams conversation id by its prefix and suffix.
func threadOfConversation(id string) Thread {
	switch {
	case strings.HasPrefix(id, "19:meeting_"):
		return ThreadMeeting
	case strings.HasSuffix(id, "@unq.gbl.spaces"), strings.HasPrefix(id, "8:"):
		return ThreadDirect
	case strings.HasSuffix(id, "@thread.tacv2"):
		return ThreadChannel
	case strings.HasSuffix(id, "@thread.v2"), strings.HasSuffix(id, "@thread.skype"):
		return ThreadGroup
	}
	return ThreadUnknown
}

// archivedStrings returns the string values of the top-level dictionary of an NSKeyedArchiver archive, by key. Anything that is not
// such an archive, or has other value types, simply yields fewer entries: the metadata is a bonus, never a reason to fail.
func archivedStrings(data []byte) map[string]string {
	out := map[string]string{}
	if len(data) == 0 {
		return out
	}
	var archive struct {
		Objects []any                `plist:"$objects"`
		Top     map[string]plist.UID `plist:"$top"`
	}
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return out
	}
	object := func(uid plist.UID) any {
		if int(uid) < len(archive.Objects) {
			return archive.Objects[uid]
		}
		return nil
	}
	root, ok := object(archive.Top["root"]).(map[string]any)
	if !ok {
		return out
	}
	keys, _ := root["NS.keys"].([]any)
	values, _ := root["NS.objects"].([]any)
	for i := 0; i < len(keys) && i < len(values); i++ {
		keyUID, ok1 := keys[i].(plist.UID)
		valueUID, ok2 := values[i].(plist.UID)
		if !ok1 || !ok2 {
			continue
		}
		key, ok1 := object(keyUID).(string)
		value, ok2 := object(valueUID).(string)
		if ok1 && ok2 {
			out[key] = value
		}
	}
	return out
}
