package ingest_test

import (
	"encoding/json"
	"testing"

	"howett.net/plist"

	"github.com/charlesrobsampson/shh/internal/ingest"
)

// keyedArchive builds what Teams puts in a notification's user info: an NSKeyedArchiver archive of a dictionary of strings, with
// the same shape as a real one (a $null object, the dictionary whose keys and values are references, then the strings).
func keyedArchive(entries map[string]string) []byte {
	objects := []any{"$null", nil}
	var keys, values []plist.UID
	for k, v := range entries {
		objects = append(objects, k)
		keys = append(keys, plist.UID(len(objects)-1))
		objects = append(objects, v)
		values = append(values, plist.UID(len(objects)-1))
	}
	objects = append(objects, map[string]any{"$classname": "NSDictionary", "$classes": []string{"NSDictionary", "NSObject"}})
	objects[1] = map[string]any{"NS.keys": keys, "NS.objects": values, "$class": plist.UID(len(objects) - 1)}
	blob, err := plist.Marshal(map[string]any{
		"$version": 100000, "$archiver": "NSKeyedArchiver", "$top": map[string]plist.UID{"root": 1}, "$objects": objects,
	}, plist.BinaryFormat)
	if err != nil {
		panic(err)
	}
	return blob
}

func options(convID string) string {
	b, _ := json.Marshal(map[string]any{"actionType": "quickReply", "convId": convID, "isSmsThread": false})
	return string(b)
}

const (
	oneToOne = "19:11111111-85e3-4fab-9f3c-aaaaaaaaaaaa_22222222-30bc-4e75-99ef-bbbbbbbbbbbb@unq.gbl.spaces"
	meeting  = "19:meeting_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefghij@thread.v2"
	group    = "19:0123456789abcdef0123456789abcdef@thread.v2"
	channel  = "19:0123456789abcdef0123456789abcdef@thread.tacv2"
)

func readThread(t *testing.T, userInfo []byte) ingest.Thread {
	t.Helper()
	d := newDB(t)
	fields := map[string]any{"titl": "Ana", "body": "hello"}
	if userInfo != nil {
		fields["usda"] = userInfo
	}
	d.add(row{recID: 1, bundle: "com.microsoft.teams2", delivered: delivered(when), data: request(fields)})
	batch, err := ingest.NewMacOS(d.path).Since(0)
	if err != nil || len(batch.Notifications) != 1 {
		t.Fatalf("Since: %v, %d notifications", err, len(batch.Notifications))
	}
	return batch.Notifications[0].Thread
}

func TestTheConversationIdTellsTheKindOfThread(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want ingest.Thread
	}{
		{"a one-to-one chat", oneToOne, ingest.ThreadDirect},
		{"an older style one-to-one id", "8:orgid:11111111-85e3-4fab-9f3c-aaaaaaaaaaaa", ingest.ThreadDirect},
		{"a meeting chat", meeting, ingest.ThreadMeeting},
		{"a group chat", group, ingest.ThreadGroup},
		{"an older group chat", "19:0123456789abcdef@thread.skype", ingest.ThreadGroup},
		{"a channel", channel, ingest.ThreadChannel},
		{"something unrecognised", "19:whatever@example.org", ingest.ThreadUnknown},
		{"nothing", "", ingest.ThreadUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readThread(t, keyedArchive(map[string]string{"default_id": tt.id, "source": "work", "default_action": "view"}))
			if got != tt.want {
				t.Errorf("thread = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheReplyOptionsAreUsedWhenThereIsNoDefaultId(t *testing.T) {
	got := readThread(t, keyedArchive(map[string]string{"textInputsend_options": options(channel)}))
	if got != ingest.ThreadChannel {
		t.Errorf("thread = %q, want channel from the convId in the reply options", got)
	}
}

func TestNotificationsWithoutUsableMetadataHaveAnUnknownThread(t *testing.T) {
	tests := map[string][]byte{
		"no user info":                                nil,
		"not a plist":                                 []byte("garbage"),
		"a plist that is no archive":                  request(map[string]any{"a": "b"}),
		"an archive of other things":                  keyedArchive(map[string]string{"kNotificationUserInfoAccountStableIdKey": "x"}),
		"an archive with a bad convId in the options": keyedArchive(map[string]string{"textInputsend_options": "{not json"}),
	}
	for name, userInfo := range tests {
		t.Run(name, func(t *testing.T) {
			if got := readThread(t, userInfo); got != ingest.ThreadUnknown {
				t.Errorf("thread = %q, want unknown (and the notification must still be read)", got)
			}
		})
	}
}
