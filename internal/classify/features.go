package classify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/charlesrobsampson/shh/internal/rules"
)

const (
	// L0Slots is the size of the block at the start of every vector reserved for the layer 0 flags, so they never share an
	// index with hashed features.
	L0Slots    = 16
	DefaultDim = 1 << 14

	// maxTextTokens bounds the work and the vector size for very long notifications.
	maxTextTokens = 128

	// featureSetRevision changes when the extraction logic itself changes, which invalidates stored weights.
	featureSetRevision = "1"
)

const (
	slotVIPSender = iota
	slotVIPApp
	slotAppPriority
	slotMention
	slotChannel
	slotDirect
	slotUrgency
	slotQuestion
)

var mention = regexp.MustCompile(`(?i)(@(you|here|channel|everyone)\b|\bmentioned you\b)`)

// Input is what the classifier knows about a notification.
type Input struct {
	Source   string
	BundleID string
	Sender   string
	Title    string
	Subtitle string
	Body     string
	Category string
}

// Vector is a sparse feature vector. Indices are strictly ascending, Values are positive, and Labels describe each entry for
// explaining a decision.
type Vector struct {
	Indices []int
	Values  []float64
	Labels  []string
}

// Features turns a notification into a vector. Weights are only valid for the Version they were trained on.
type Features interface {
	Extract(Input) Vector
	Dim() int
	Version() string
}

type Option func(*L0L1)

// WithDim sets the vector size; it must leave room for hashed features after the L0 block.
func WithDim(dim int) Option {
	return func(f *L0L1) { f.dim = dim }
}

// L0L1 combines layer 0 (rules and priors) with layer 1 (hashed words, phrases, sender, app and category).
type L0L1 struct {
	dim         int
	rules       rules.Rules
	vipSenders  []string
	vipApps     []string
	priorities  map[string]priority
	versionText string
}

type priority struct {
	name  string
	value float64
}

func NewL0L1(r rules.Rules, opts ...Option) (*L0L1, error) {
	f := &L0L1{dim: DefaultDim, rules: r, priorities: map[string]priority{}}
	for _, opt := range opts {
		opt(f)
	}
	if f.dim <= L0Slots {
		return nil, fmt.Errorf("vector size %d leaves no room for hashed features after the %d layer 0 slots", f.dim, L0Slots)
	}
	f.vipSenders = lowered(r.VIPSenders)
	f.vipApps = lowered(r.VIPApps)
	for name, value := range r.AppPriority {
		f.priorities[strings.ToLower(name)] = priority{name, value}
	}
	f.versionText = f.fingerprint()
	return f, nil
}

func (f *L0L1) Dim() int        { return f.dim }
func (f *L0L1) Version() string { return f.versionText }

func (f *L0L1) Extract(in Input) Vector {
	acc := accumulator{entries: map[int]*entry{}}
	text := strings.Join([]string{in.Title, in.Subtitle, in.Body}, " ")
	sender := strings.ToLower(strings.TrimSpace(in.Sender))
	source := strings.ToLower(strings.TrimSpace(in.Source))

	if sender != "" && slices.Contains(f.vipSenders, sender) {
		acc.add(slotVIPSender, 1, "vip sender")
	}
	if (source != "" && slices.Contains(f.vipApps, source)) || (in.BundleID != "" && slices.Contains(f.vipApps, strings.ToLower(in.BundleID))) {
		acc.add(slotVIPApp, 1, "vip app")
	}
	if p, ok := f.priorities[source]; ok && p.value > 0 {
		acc.add(slotAppPriority, p.value, "app priority: "+p.name)
	}
	if mention.MatchString(text) {
		acc.add(slotMention, 1, "mentions you")
	}
	// Starting heuristics: real Teams chats here carry only a title and body, so channel messages are guessed from text.
	headline := in.Title + " " + in.Subtitle
	channel := strings.Contains(headline, "#") || strings.Contains(strings.ToLower(headline), " in ")
	switch {
	case channel:
		acc.add(slotChannel, 1, "channel message")
	case in.Title != "":
		acc.add(slotDirect, 1, "direct message")
	}
	if strings.Contains(text, "?") {
		acc.add(slotQuestion, 1, "question")
	}
	for _, u := range f.rules.Urgency {
		if u.Regexp.MatchString(text) {
			acc.add(slotUrgency, 1, "urgency matched")
			acc.add(f.hashed("u:", u.Name), 1, "urgency: "+u.Name)
		}
	}

	tokens := tokenize(text)
	for i, token := range tokens {
		acc.add(f.hashed("w:", token), 1, "word: "+token)
		if i > 0 {
			phrase := tokens[i-1] + " " + token
			acc.add(f.hashed("b:", phrase), 1, "phrase: "+phrase)
		}
	}
	if sender != "" {
		acc.add(f.hashed("s:", sender), 1, "sender: "+sender)
	}
	if source != "" {
		acc.add(f.hashed("a:", source), 1, "app: "+source)
	}
	if category := strings.ToLower(strings.TrimSpace(in.Category)); category != "" {
		acc.add(f.hashed("c:", category), 1, "category: "+category)
	}
	return acc.vector()
}

// hashed maps a namespaced feature name into the part of the vector after the L0 block.
func (f *L0L1) hashed(namespace, name string) int {
	h := fnv.New64a()
	h.Write([]byte(namespace + name))
	return L0Slots + int(h.Sum64()%uint64(f.dim-L0Slots))
}

func (f *L0L1) fingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "revision=%s dim=%d tokens=%d\n", featureSetRevision, f.dim, maxTextTokens)
	urgency := make([]string, len(f.rules.Urgency))
	for i, u := range f.rules.Urgency {
		urgency[i] = fmt.Sprintf("%s|%s|%v", u.Name, u.Regexp.String(), u.Boost)
	}
	sort.Strings(urgency)
	fmt.Fprintf(&b, "urgency=%q\n", urgency)
	fmt.Fprintf(&b, "vip senders=%q\n", sorted(f.vipSenders))
	fmt.Fprintf(&b, "vip apps=%q\n", sorted(f.vipApps))
	keys := make([]string, 0, len(f.priorities))
	for key := range f.priorities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&b, "priority %s=%v\n", key, f.priorities[key].value)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "l0l1-" + hex.EncodeToString(sum[:])[:12]
}

func tokenize(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var tokens []string
	for _, w := range words {
		if len([]rune(w)) >= 2 {
			tokens = append(tokens, w)
		}
	}
	if len(tokens) > maxTextTokens {
		tokens = tokens[:maxTextTokens]
	}
	return tokens
}

func lowered(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

type entry struct {
	value  float64
	labels []string
}

// accumulator merges features that land on the same index: values add up and labels are joined.
type accumulator struct {
	entries map[int]*entry
}

func (a *accumulator) add(index int, value float64, label string) {
	e := a.entries[index]
	if e == nil {
		a.entries[index] = &entry{value: value, labels: []string{label}}
		return
	}
	// The same feature seen again (a repeated word) is still one feature; a different one sharing the index adds up.
	if slices.Contains(e.labels, label) {
		return
	}
	e.value += value
	e.labels = append(e.labels, label)
}

func (a *accumulator) vector() Vector {
	var v Vector
	indices := make([]int, 0, len(a.entries))
	for index := range a.entries {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		e := a.entries[index]
		v.Indices = append(v.Indices, index)
		v.Values = append(v.Values, e.value)
		v.Labels = append(v.Labels, strings.Join(e.labels, " | "))
	}
	return v
}

