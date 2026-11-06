package classify_test

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/rules"
)

func defaultRules(t *testing.T) rules.Rules {
	t.Helper()
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func extractor(t *testing.T, r rules.Rules, opts ...classify.Option) *classify.L0L1 {
	t.Helper()
	f, err := classify.NewL0L1(r, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func labels(v classify.Vector) map[string]float64 {
	out := map[string]float64{}
	for i, label := range v.Labels {
		out[label] = v.Values[i]
	}
	return out
}

func has(v classify.Vector, label string) bool {
	_, ok := labels(v)[label]
	return ok
}

// differences lists labels present in exactly one of the two vectors.
func differences(a, b classify.Vector) []string {
	la, lb := labels(a), labels(b)
	var out []string
	for l := range la {
		if _, ok := lb[l]; !ok {
			out = append(out, l)
		}
	}
	for l := range lb {
		if _, ok := la[l]; !ok {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

var teamsMessage = classify.Input{Source: "Teams", BundleID: "com.microsoft.teams2", Sender: "Ana", Title: "Ana", Body: "the build is green again"}

func TestExtractionIsDeterministic(t *testing.T) {
	r := defaultRules(t)
	first := extractor(t, r).Extract(teamsMessage)
	second := extractor(t, r).Extract(teamsMessage) // a separate instance
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two extractions differ:\n%+v\n%+v", first, second)
	}
}

func TestVectorInvariants(t *testing.T) {
	f := extractor(t, defaultRules(t))
	inputs := []classify.Input{
		teamsMessage,
		{Source: "Outlook", Sender: "Bo", Title: "Re: prod is down!", Body: "URGENT?? @here please look now, repeat repeat repeat", Category: "OLAlertsNotificationCategory"},
		{Title: "Déploiement échoué 🚀", Body: "日本語のテキスト and emoji 🔥"},
		{},
	}
	for _, in := range inputs {
		v := f.Extract(in)
		if len(v.Indices) != len(v.Values) || len(v.Indices) != len(v.Labels) {
			t.Fatalf("parallel slices differ in length: %d %d %d", len(v.Indices), len(v.Values), len(v.Labels))
		}
		for i, idx := range v.Indices {
			if idx < 0 || idx >= f.Dim() {
				t.Errorf("index %d outside [0, %d)", idx, f.Dim())
			}
			if i > 0 && idx <= v.Indices[i-1] {
				t.Errorf("indices are not strictly ascending: %v", v.Indices)
			}
			if v.Values[i] <= 0 || math.IsNaN(v.Values[i]) || math.IsInf(v.Values[i], 0) {
				t.Errorf("value %v for %q is not a positive finite number", v.Values[i], v.Labels[i])
			}
		}
	}
}

func TestEmptyInputGivesAnEmptyVector(t *testing.T) {
	if v := extractor(t, defaultRules(t)).Extract(classify.Input{}); len(v.Indices) != 0 {
		t.Fatalf("empty input produced features: %v", v.Labels)
	}
}

func TestLayerZeroFlags(t *testing.T) {
	r := defaultRules(t)
	r.VIPSenders = []string{"jane boss"}
	r.VIPApps = []string{"pagerduty"}
	r.AppPriority["PagerDuty"] = 0.95
	f := extractor(t, r)

	tests := []struct {
		name    string
		in      classify.Input
		want    []string
		wantNot []string
	}{
		{"vip sender, any case", classify.Input{Sender: "JANE Boss", Title: "JANE Boss", Body: "hi"}, []string{"vip sender"}, nil},
		{"not a vip", classify.Input{Sender: "Ana", Title: "Ana", Body: "hi"}, nil, []string{"vip sender", "vip app"}},
		{"vip app by source", classify.Input{Source: "PagerDuty", Body: "hi"}, []string{"vip app"}, nil},
		{"vip app by bundle id", classify.Input{BundleID: "PagerDuty", Body: "hi"}, []string{"vip app"}, nil},
		{"app priority from the rules", classify.Input{Source: "Teams", Body: "hi"}, []string{"app priority: Teams"}, nil},
		{"app priority is not case sensitive", classify.Input{Source: "teams", Body: "hi"}, []string{"app priority: Teams"}, nil},
		{"unknown app has no priority", classify.Input{Source: "Paw", Body: "hi"}, nil, []string{"app priority: Paw"}},
		{"mention with @here", classify.Input{Body: "@here please look"}, []string{"mentions you"}, nil},
		{"mention in words", classify.Input{Body: "Ana mentioned you in a thread"}, []string{"mentions you"}, nil},
		{"no mention", classify.Input{Body: "hello there"}, nil, []string{"mentions you"}},
		{"channel message by title", classify.Input{Title: "Ana in General", Body: "x"}, []string{"channel message"}, []string{"direct message"}},
		{"channel message by hash", classify.Input{Title: "Ana", Subtitle: "#platform", Body: "x"}, []string{"channel message"}, []string{"direct message"}},
		{"direct message", classify.Input{Title: "Ana", Body: "x"}, []string{"direct message"}, []string{"channel message"}},
		{"urgency words, one flag per rule", classify.Input{Body: "prod outage right now"}, []string{"urgency: outage", "urgency: prod", "urgency: now", "urgency matched"}, nil},
		{"no urgency", classify.Input{Body: "lunch menu"}, nil, []string{"urgency matched"}},
		{"a question", classify.Input{Body: "can you review this?"}, []string{"question"}, nil},
		{"not a question", classify.Input{Body: "can you review this."}, nil, []string{"question"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := f.Extract(tt.in)
			for _, label := range tt.want {
				if !has(v, label) {
					t.Errorf("missing feature %q; have %v", label, v.Labels)
				}
			}
			for _, label := range tt.wantNot {
				if has(v, label) {
					t.Errorf("unexpected feature %q", label)
				}
			}
		})
	}

	t.Run("app priority carries its value", func(t *testing.T) {
		got := labels(f.Extract(classify.Input{Source: "PagerDuty", Body: "hi"}))["app priority: PagerDuty"]
		if got != 0.95 {
			t.Errorf("value = %v, want the 0.95 from the rules", got)
		}
	})
}

func TestChangingOnlyTheSenderChangesOnlyTheSenderFeature(t *testing.T) {
	f := extractor(t, defaultRules(t))
	a, b := teamsMessage, teamsMessage
	b.Sender = "Bo"
	got := differences(f.Extract(a), f.Extract(b))
	if want := []string{"sender: ana", "sender: bo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("differences = %v, want %v", got, want)
	}
}

func TestChangingOnlyTheAppChangesOnlyTheAppFeatures(t *testing.T) {
	f := extractor(t, defaultRules(t))
	a, b := teamsMessage, teamsMessage
	b.Source = "Slack"
	got := differences(f.Extract(a), f.Extract(b))
	want := []string{"app priority: Slack", "app priority: Teams", "app: slack", "app: teams"}
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("differences = %v, want %v", got, want)
	}
}

func TestChangingOnlyTheCategoryChangesOnlyTheCategoryFeature(t *testing.T) {
	f := extractor(t, defaultRules(t))
	a, b := teamsMessage, teamsMessage
	a.Category, b.Category = "OLAlertsNotificationCategory", "OLAlertsNotificationReminders"
	got := differences(f.Extract(a), f.Extract(b))
	want := []string{"category: olalertsnotificationcategory", "category: olalertsnotificationreminders"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("differences = %v, want %v", got, want)
	}
}

func TestTextFeaturesAreWordsAndPhrases(t *testing.T) {
	f := extractor(t, defaultRules(t))
	v := f.Extract(classify.Input{Title: "Payments", Body: "Server is DOWN, again!"})
	for _, label := range []string{"word: payments", "word: server", "word: down", "word: again", "phrase: server is", "phrase: is down"} {
		if !has(v, label) {
			t.Errorf("missing %q; have %v", label, v.Labels)
		}
	}
	if has(v, "word: a") {
		t.Error("single-letter tokens should be dropped")
	}
}

func TestRepeatedWordsCountOnce(t *testing.T) {
	f := extractor(t, defaultRules(t))
	v := f.Extract(classify.Input{Body: "deploy deploy deploy deploy"})
	if got := labels(v)["word: deploy"]; got != 1 {
		t.Errorf("value for a repeated word = %v, want 1", got)
	}
}

func TestVeryLongTextIsBounded(t *testing.T) {
	f := extractor(t, defaultRules(t))
	words := make([]string, 5000)
	for i := range words {
		words[i] = "word" + strings.Repeat("x", i%40) + string(rune('a'+i%26))
	}
	v := f.Extract(classify.Input{Body: strings.Join(words, " ")})
	if len(v.Indices) > 400 {
		t.Errorf("a long text produced %d features, want a bounded number", len(v.Indices))
	}
}

func TestHashCollisionsAreHandled(t *testing.T) {
	// With one hashed bucket every text, sender, app and category feature lands in the same place.
	f := extractor(t, defaultRules(t), classify.WithDim(classify.L0Slots+1))
	in := classify.Input{
		Source: "Teams", Sender: "Ana", Title: "Ana", Category: "chat",
		Body: "the production server is down and customers cannot log in, please help right now",
	}
	v := f.Extract(in) // must not panic
	bucket := f.Dim() - 1
	var inBucket int
	for i, idx := range v.Indices {
		if idx < 0 || idx >= f.Dim() {
			t.Fatalf("index %d out of range", idx)
		}
		if idx == bucket {
			inBucket++
			if v.Values[i] < 2 {
				t.Errorf("colliding features should add up, got %v", v.Values[i])
			}
		}
	}
	if inBucket != 1 {
		t.Errorf("entries in the shared bucket = %d, want exactly 1", inBucket)
	}
	if !has(v, "urgency matched") {
		t.Error("layer 0 features must keep their own slots and never collide with hashed ones")
	}
}

func TestDimensionMustLeaveRoomForHashedFeatures(t *testing.T) {
	for _, dim := range []int{0, -5, classify.L0Slots} {
		if _, err := classify.NewL0L1(defaultRules(t), classify.WithDim(dim)); err == nil {
			t.Errorf("WithDim(%d) accepted", dim)
		}
	}
}

func TestVersionChangesWithTheFeatureConfiguration(t *testing.T) {
	base := defaultRules(t)
	baseVersion := extractor(t, base).Version()

	if got := extractor(t, defaultRules(t)).Version(); got != baseVersion {
		t.Errorf("identical configuration gave %q and %q", baseVersion, got)
	}
	if !strings.HasPrefix(baseVersion, "l0l1-") {
		t.Errorf("version %q should start with the feature set name", baseVersion)
	}

	changes := map[string]func(*rules.Rules){
		"a new VIP sender":     func(r *rules.Rules) { r.VIPSenders = append(r.VIPSenders, "someone new") },
		"a new VIP app":        func(r *rules.Rules) { r.VIPApps = append(r.VIPApps, "pagerduty") },
		"an app priority":      func(r *rules.Rules) { r.AppPriority["Teams"] = 0.99 },
		"a new app priority":   func(r *rules.Rules) { r.AppPriority["PagerDuty"] = 0.9 },
		"an urgency boost":     func(r *rules.Rules) { r.Urgency[0].Boost = 0.11 },
		"an urgency rule gone": func(r *rules.Rules) { r.Urgency = r.Urgency[1:] },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r := defaultRules(t)
			change(&r)
			if got := extractor(t, r).Version(); got == baseVersion {
				t.Errorf("version did not change for %s", name)
			}
		})
	}
	t.Run("a different vector size", func(t *testing.T) {
		if got := extractor(t, base, classify.WithDim(4096)).Version(); got == baseVersion {
			t.Error("version did not change with the dimension")
		}
	})
}
