package classify_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/store"
)

// A tiny model in the real Hugging Face layout: a BERT WordPiece tokenizer.json and a safetensors file holding the embedding
// table. Urgent words point along the first axis, newsletter words along the second, and the rest are neutral.
var fixtureWords = []struct {
	token  string
	vector []float32
}{
	{"[PAD]", []float32{0, 0, 0, 0}},
	{"[UNK]", []float32{0, 0, 0, 0}},
	{"[CLS]", []float32{0, 0, 0, 0}},
	{"[SEP]", []float32{0, 0, 0, 0}},
	{"server", []float32{0.6, 0, 0.8, 0}},
	{"down", []float32{1, 0.1, 0, 0}},
	{"outage", []float32{0.9, 0, 0, 0.2}},
	{"incident", []float32{0.8, 0.1, 0.1, 0}},
	{"urgent", []float32{1, 0, 0, 0.1}},
	{"now", []float32{0.7, 0, 0, 0.7}},
	{"newsletter", []float32{0, 1, 0, 0}},
	{"tips", []float32{0.1, 0.9, 0, 0}},
	{"weekly", []float32{0, 0.8, 0.1, 0}},
	{"digest", []float32{0, 0.9, 0, 0.1}},
	{"sale", []float32{0, 0.7, 0, 0.3}},
	{"unsubscribe", []float32{0, 1, 0, 0}},
	{"the", []float32{0.1, 0.1, 0.1, 0.1}},
	{"is", []float32{0.1, 0.1, 0.1, 0.1}},
	{"cafe", []float32{0.2, 0.2, 0.9, 0.2}},
	{"hello", []float32{0.1, 0.1, 0.2, 0.9}},
	{"##s", []float32{0.3, 0.3, 0.3, 0.3}},
	{"!", []float32{0, 0, 0.1, 0.1}},
	{",", []float32{0, 0, 0.1, 0.1}},
	{"日", []float32{0, 0, 0, 1}},
}

func fixtureFiles(t *testing.T) (safetensors, tokenizerJSON []byte) {
	t.Helper()
	vocab := map[string]int{}
	var table []byte
	for i, w := range fixtureWords {
		vocab[w.token] = i
		for _, v := range w.vector {
			table = binary.LittleEndian.AppendUint32(table, math.Float32bits(v))
		}
	}
	header, _ := json.Marshal(map[string]any{
		"embeddings": map[string]any{"dtype": "F32", "shape": []int{len(fixtureWords), 4}, "data_offsets": []int{0, len(table)}},
	})
	safetensors = binary.LittleEndian.AppendUint64(nil, uint64(len(header)))
	safetensors = append(append(safetensors, header...), table...)

	tokenizerJSON, _ = json.Marshal(map[string]any{
		"normalizer":    map[string]any{"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true},
		"pre_tokenizer": map[string]any{"type": "BertPreTokenizer"},
		"model": map[string]any{
			"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100, "vocab": vocab,
		},
	})
	return safetensors, tokenizerJSON
}

func fixtureAsset(t *testing.T) []byte {
	t.Helper()
	st, tok := fixtureFiles(t)
	asset, err := classify.BuildStaticModel("fixture-int8", st, tok)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func fixtureEmbedder(t *testing.T) *classify.StaticEmbedder {
	t.Helper()
	e, err := classify.NewStaticEmbedder(fixtureAsset(t))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func normalized(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	n = math.Sqrt(n)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

func rowOf(token string) []float32 {
	for _, w := range fixtureWords {
		if w.token == token {
			return w.vector
		}
	}
	panic("no such token " + token)
}

func TestTheEmbedderReportsItsIDAndSize(t *testing.T) {
	e := fixtureEmbedder(t)
	if e.ID() != "fixture-int8" || e.Dim() != 4 {
		t.Fatalf("ID=%q Dim=%d, want fixture-int8 and 4", e.ID(), e.Dim())
	}
}

func TestTextIsTokenizedLikeBERT(t *testing.T) {
	e := fixtureEmbedder(t)
	// Each case embeds the same as the text made only of the listed known tokens.
	tests := []struct {
		name   string
		text   string
		tokens []string
	}{
		{"plain words", "server down", []string{"server", "down"}},
		{"case is ignored", "SERVER Down", []string{"server", "down"}},
		{"punctuation is split off", "down!", []string{"down", "!"}},
		{"commas too", "server,down", []string{"server", ",", "down"}},
		{"accents are stripped", "Café", []string{"cafe"}},
		{"word pieces", "servers", []string{"server", "##s"}},
		{"unknown words are dropped", "zzzz server qqqq", []string{"server"}},
		{"extra whitespace and newlines", "  server \n\t down  ", []string{"server", "down"}},
		{"CJK characters stand alone", "hello日", []string{"hello", "日"}},
		{"control characters are removed", "serv\x00er down", []string{"server", "down"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.Embed(tt.text)
			want := meanOf(tt.tokens)
			if c := cosine(got, want); c < 0.999 {
				t.Errorf("Embed(%q) is not the mean of %v (cosine %v)", tt.text, tt.tokens, c)
			}
		})
	}
}

func meanOf(tokens []string) []float32 {
	sum := make([]float32, 4)
	for _, tok := range tokens {
		for i, v := range rowOf(tok) {
			sum[i] += v
		}
	}
	return normalized(sum)
}

func TestEmbeddingsAreUnitLength(t *testing.T) {
	e := fixtureEmbedder(t)
	for _, text := range []string{"server down", "the weekly digest", "hello"} {
		v := e.Embed(text)
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		if math.Abs(math.Sqrt(n)-1) > 1e-5 {
			t.Errorf("Embed(%q) has length %v, want 1", text, math.Sqrt(n))
		}
	}
}

func TestEmbeddingIsDeterministic(t *testing.T) {
	a, b := fixtureEmbedder(t), fixtureEmbedder(t)
	for _, text := range []string{"server down now", "Café newsletter!"} {
		va, vb := a.Embed(text), b.Embed(text)
		for i := range va {
			if va[i] != vb[i] {
				t.Fatalf("Embed(%q) differs between instances: %v vs %v", text, va, vb)
			}
		}
	}
}

func TestEmptyOrUnknownTextGivesAllZeros(t *testing.T) {
	e := fixtureEmbedder(t)
	for _, text := range []string{"", "   ", "\n", "zzzz qqqq", "[UNK]"} {
		v := e.Embed(text)
		if len(v) != e.Dim() {
			t.Fatalf("Embed(%q) has %d values, want %d", text, len(v), e.Dim())
		}
		for _, x := range v {
			if x != 0 {
				t.Errorf("Embed(%q) = %v, want all zeros (and no NaN)", text, v)
				break
			}
		}
	}
}

func TestVeryLongWordsAreIgnored(t *testing.T) {
	e := fixtureEmbedder(t)
	if got, want := e.Embed("server "+strings.Repeat("x", 500)), e.Embed("server"); cosine(got, want) < 0.999 {
		t.Errorf("a 500-letter word changed the embedding")
	}
}

func TestQuantizationBarelyChangesTheEmbedding(t *testing.T) {
	e := fixtureEmbedder(t)
	for _, tokens := range [][]string{{"server", "down"}, {"newsletter", "tips", "weekly"}, {"hello"}} {
		if c := cosine(e.Embed(strings.Join(tokens, " ")), meanOf(tokens)); c < 0.9999 {
			t.Errorf("%v: int8 embedding has cosine %v to the exact one", tokens, c)
		}
	}
}

func TestRelatedWordsAreCloserThanUnrelatedOnes(t *testing.T) {
	e := fixtureEmbedder(t)
	urgent := e.Embed("server outage")
	if cosine(urgent, e.Embed("urgent incident")) <= cosine(urgent, e.Embed("weekly newsletter tips")) {
		t.Error("an urgent text should be closer to another urgent text than to a newsletter")
	}
}

func TestBuildingRejectsUnsupportedFiles(t *testing.T) {
	st, tok := fixtureFiles(t)
	tests := []struct {
		name string
		st   []byte
		tok  []byte
	}{
		{"truncated safetensors", st[:20], tok},
		{"not safetensors", []byte("hello hello hello hello"), tok},
		{"not json", st, []byte("{")},
		{"a tokenizer that is not WordPiece", st, []byte(`{"model":{"type":"BPE","vocab":{}}}`)},
		{"a vocabulary that does not fit the table", st, []byte(`{"model":{"type":"WordPiece","unk_token":"[UNK]","vocab":{"a":0,"[UNK]":1}},"normalizer":{"type":"BertNormalizer","lowercase":true}}`)},
		{"a vocabulary id beyond the table", st, []byte(`{"model":{"type":"WordPiece","unk_token":"[UNK]","vocab":{"[UNK]":99999}},"normalizer":{"type":"BertNormalizer","lowercase":true}}`)},
		{"a tokenizer that does not lowercase", st, func() []byte {
			var doc map[string]any
			json.Unmarshal(tok, &doc)
			doc["normalizer"].(map[string]any)["lowercase"] = false
			out, _ := json.Marshal(doc)
			return out
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := classify.BuildStaticModel("x", tt.st, tt.tok); err == nil {
				t.Fatal("BuildStaticModel accepted it")
			}
		})
	}
}

func TestLoadingRejectsDamagedAssets(t *testing.T) {
	asset := fixtureAsset(t)
	tests := []struct {
		name  string
		asset []byte
	}{
		{"empty", nil},
		{"wrong magic", append([]byte("notashhm"), asset[8:]...)},
		{"truncated", asset[:len(asset)-10]},
		{"trailing bytes", append(append([]byte{}, asset...), 1, 2, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := classify.NewStaticEmbedder(tt.asset); err == nil {
				t.Fatal("NewStaticEmbedder accepted a damaged asset")
			}
		})
	}
}

func writeAsset(t *testing.T, dir string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, classify.ModelFileName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestTheModelIsLoadedFromTheModelsDirectory(t *testing.T) {
	asset := fixtureAsset(t)
	dir := t.TempDir()
	writeAsset(t, dir, asset)

	e, source, err := classify.ResolveEmbedder(dir, sum(asset))
	if err != nil || e == nil {
		t.Fatalf("ResolveEmbedder = %v, %q, %v", e, source, err)
	}
	if e.ID() != "fixture-int8" || !strings.Contains(source, dir) {
		t.Errorf("id=%q source=%q, want the fixture model from %s", e.ID(), source, dir)
	}
}

func TestAMissingModelMeansLayerTwoIsOff(t *testing.T) {
	e, _, err := classify.ResolveEmbedder(t.TempDir(), sum(fixtureAsset(t)))
	if err != classify.ErrNoEmbeddingModel || e != nil {
		t.Fatalf("ResolveEmbedder = %v, %v; want nil and ErrNoEmbeddingModel", e, err)
	}
}

func TestAModelWithTheWrongChecksumIsRejected(t *testing.T) {
	asset := fixtureAsset(t)
	dir := t.TempDir()
	writeAsset(t, dir, asset)

	e, _, err := classify.ResolveEmbedder(dir, sum([]byte("some other file")))
	if err == nil || err == classify.ErrNoEmbeddingModel || e != nil {
		t.Fatalf("ResolveEmbedder = %v, %v; want a checksum error, not a silent fallback", e, err)
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error %q should say it is a checksum problem", err)
	}
}

func TestACorruptedModelFileIsAnErrorEvenWithAMatchingChecksum(t *testing.T) {
	garbage := []byte("definitely not a model asset")
	dir := t.TempDir()
	writeAsset(t, dir, garbage)
	if _, _, err := classify.ResolveEmbedder(dir, sum(garbage)); err == nil || err == classify.ErrNoEmbeddingModel {
		t.Fatalf("err = %v, want a load error", err)
	}
}

func TestWithoutTheEmbedTagNothingIsEmbedded(t *testing.T) {
	if classify.HasEmbeddedModel() {
		t.Skip("built with the embedmodel tag")
	}
	if _, _, err := classify.ResolveEmbedder("", sum(fixtureAsset(t))); err != classify.ErrNoEmbeddingModel {
		t.Fatalf("err = %v, want ErrNoEmbeddingModel", err)
	}
}

// --- the embedding as classifier features ---

func embeddingFeatures(t *testing.T) *classify.L0L1 {
	t.Helper()
	f, err := classify.NewL0L1(defaultRules(t), classify.WithEmbedder(fixtureEmbedder(t)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTheEmbeddingIsAppendedAfterTheHashedFeatures(t *testing.T) {
	plain := extractor(t, defaultRules(t))
	f := embeddingFeatures(t)
	if f.Dim() != plain.Dim()+4 {
		t.Fatalf("Dim = %d, want the hashed size %d plus 4 embedding values", f.Dim(), plain.Dim())
	}
	in := classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "server down now"}
	v := f.Extract(in)
	base := plain.Extract(in)

	var dense []int
	for i, idx := range v.Indices {
		if i > 0 && idx <= v.Indices[i-1] {
			t.Fatalf("indices are not ascending: %v", v.Indices)
		}
		if idx >= plain.Dim() {
			dense = append(dense, i)
			if v.Labels[i] != "meaning" {
				t.Errorf("dense entry labelled %q, want meaning", v.Labels[i])
			}
		}
	}
	if len(dense) == 0 || len(v.Indices)-len(dense) != len(base.Indices) {
		t.Fatalf("%d dense entries and %d others; the others must equal the hashed features (%d)", len(dense), len(v.Indices)-len(dense), len(base.Indices))
	}
	for i := range base.Indices {
		if v.Indices[i] != base.Indices[i] || v.Values[i] != base.Values[i] {
			t.Fatalf("hashed features changed when the embedding was added")
		}
	}
}

func TestEmptyTextAddsNoDenseFeatures(t *testing.T) {
	f := embeddingFeatures(t)
	v := f.Extract(classify.Input{Source: "Teams", Sender: "Ana"})
	for _, idx := range v.Indices {
		if idx >= f.Dim()-4 {
			t.Fatalf("an empty text produced a dense feature at %d", idx)
		}
	}
}

func TestTheVersionNamesTheEmbeddingModel(t *testing.T) {
	plain := extractor(t, defaultRules(t))
	f := embeddingFeatures(t)
	if f.Version() == plain.Version() {
		t.Fatal("adding the embedding did not change the feature version")
	}
	if !strings.HasSuffix(f.Version(), "+fixture-int8") {
		t.Errorf("version %q should end with the model id", f.Version())
	}
	other, err := classify.NewL0L1(defaultRules(t), classify.WithEmbedder(namedEmbedder{fixtureEmbedder(t), "other-model"}))
	if err != nil {
		t.Fatal(err)
	}
	if other.Version() == f.Version() {
		t.Error("a different embedding model must give a different version")
	}
}

type namedEmbedder struct {
	classify.Embedder
	id string
}

func (n namedEmbedder) ID() string { return n.id }

func TestTheEmbeddingGeneralisesToWordsTheModelNeverSawInTraining(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	f := embeddingFeatures(t)
	m, err := classify.NewModel(s, f)
	if err != nil {
		t.Fatal(err)
	}
	vec := func(body string) classify.Vector { return f.Extract(classify.Input{Body: body}) }

	for round := 0; round < 40; round++ {
		for _, body := range []string{"server down now", "urgent outage", "the server is down"} {
			m.Train(0, vec(body), classify.ExactLabel(90))
		}
		for _, body := range []string{"weekly newsletter tips", "newsletter digest", "unsubscribe from the weekly digest"} {
			m.Train(0, vec(body), classify.ExactLabel(10))
		}
	}

	// "incident" and "sale" never appeared in training: only their embeddings tie them to a class.
	urgent, promo := m.Predict(vec("incident")), m.Predict(vec("sale"))
	if urgent <= 0.5 || promo >= 0.5 || urgent <= promo {
		t.Fatalf("unseen urgent word scores %v, unseen promotional word %v; want them on their own sides of 0.5", urgent, promo)
	}
	if m.Predict(vec("server down now")) <= m.Predict(vec("weekly newsletter tips")) {
		t.Error("trained examples are not ordered correctly")
	}
}
