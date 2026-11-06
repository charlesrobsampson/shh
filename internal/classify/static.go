package classify

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ModelFileName is the quantized static embedding model shh uses; see docs/spikes/05-embeddings.md.
	ModelFileName = "potion-base-8M-int8.bin"

	assetMagic = "shhstat1"
)

// DefaultModelSHA256 is the checksum of the published ModelFileName asset (the models-v1 release, built by `task model:build`).
const DefaultModelSHA256 = "bdd4b106b293635d9a1b60eb9f9786f8fd47a7f4215a8f7c5f8fa0984d4356bd"

// ErrNoEmbeddingModel means no embedding model is available, so layer 2 stays off.
var ErrNoEmbeddingModel = errors.New("no embedding model found (run shh init to download it)")

// Embedder turns text into a fixed-size vector.
type Embedder interface {
	// ID names the model and quantization; it becomes part of the feature version.
	ID() string
	Dim() int
	// Embed returns a unit-length vector, or all zeros when the text has no known words.
	Embed(text string) []float32
}

// StaticEmbedder is a Model2Vec static embedding model: look up each token's row and average them.
type StaticEmbedder struct {
	id        string
	dim       int
	tokenizer wordPiece
	scales    []float32
	rows      []int8
}

// Asset layout (little endian): magic "shhstat1"; id (uint16 length + bytes); dim, rows, unk id, max word length (uint32 each);
// the vocabulary in id order (uint16 length + bytes each); then per row a float32 scale followed by dim int8 values.

// BuildStaticModel converts a Model2Vec model, given as its safetensors and tokenizer.json files, into shh's compact asset.
func BuildStaticModel(id string, safetensors, tokenizerJSON []byte) ([]byte, error) {
	table, rows, dim, err := readSafetensors(safetensors)
	if err != nil {
		return nil, err
	}
	var tok struct {
		Normalizer struct {
			Type      string `json:"type"`
			Lowercase bool   `json:"lowercase"`
		} `json:"normalizer"`
		PreTokenizer struct {
			Type string `json:"type"`
		} `json:"pre_tokenizer"`
		Model struct {
			Type     string         `json:"type"`
			Unk      string         `json:"unk_token"`
			MaxChars int            `json:"max_input_chars_per_word"`
			Vocab    map[string]int `json:"vocab"`
		} `json:"model"`
	}
	if err := json.Unmarshal(tokenizerJSON, &tok); err != nil {
		return nil, fmt.Errorf("tokenizer.json: %w", err)
	}
	switch {
	case tok.Model.Type != "WordPiece":
		return nil, fmt.Errorf("tokenizer model %q is not supported, only WordPiece", tok.Model.Type)
	case tok.Normalizer.Type != "BertNormalizer" || !tok.Normalizer.Lowercase:
		return nil, errors.New("only the lowercasing BertNormalizer is supported")
	case tok.PreTokenizer.Type != "BertPreTokenizer":
		return nil, fmt.Errorf("pre-tokenizer %q is not supported, only BertPreTokenizer", tok.PreTokenizer.Type)
	case len(tok.Model.Vocab) != rows:
		return nil, fmt.Errorf("the vocabulary has %d entries but the embedding table has %d rows", len(tok.Model.Vocab), rows)
	}
	unk, ok := tok.Model.Vocab[tok.Model.Unk]
	if !ok {
		return nil, fmt.Errorf("unknown-word token %q is not in the vocabulary", tok.Model.Unk)
	}
	tokens := make([]string, rows)
	for token, tokenID := range tok.Model.Vocab {
		if tokenID < 0 || tokenID >= rows || tokens[tokenID] != "" {
			return nil, fmt.Errorf("vocabulary id %d for %q is out of range or repeated", tokenID, token)
		}
		tokens[tokenID] = token
	}
	maxChars := tok.Model.MaxChars
	if maxChars == 0 {
		maxChars = 100
	}

	var out []byte
	out = append(out, assetMagic...)
	out = appendString(out, id)
	for _, v := range []int{dim, rows, unk, maxChars} {
		out = binary.LittleEndian.AppendUint32(out, uint32(v))
	}
	for _, token := range tokens {
		out = appendString(out, token)
	}
	for r := 0; r < rows; r++ {
		row := table[r*dim : (r+1)*dim]
		var biggest float64
		for _, v := range row {
			biggest = math.Max(biggest, math.Abs(float64(v)))
		}
		scale := float32(biggest / 127)
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(scale))
		for _, v := range row {
			q := int8(0)
			if scale != 0 {
				q = int8(math.Round(float64(v) / float64(scale)))
			}
			out = append(out, byte(q))
		}
	}
	return out, nil
}

func appendString(out []byte, s string) []byte {
	out = binary.LittleEndian.AppendUint16(out, uint16(len(s)))
	return append(out, s...)
}

func readSafetensors(data []byte) (table []float32, rows, dim int, err error) {
	if len(data) < 8 {
		return nil, 0, 0, errors.New("safetensors file is too short")
	}
	headerLen := binary.LittleEndian.Uint64(data)
	if headerLen > uint64(len(data)-8) {
		return nil, 0, 0, errors.New("safetensors header is longer than the file")
	}
	var header map[string]struct {
		DType   string `json:"dtype"`
		Shape   []int  `json:"shape"`
		Offsets [2]int `json:"data_offsets"`
	}
	if err := json.Unmarshal(data[8:8+headerLen], &header); err != nil {
		return nil, 0, 0, fmt.Errorf("safetensors header: %w", err)
	}
	e, ok := header["embeddings"]
	if !ok || e.DType != "F32" || len(e.Shape) != 2 {
		return nil, 0, 0, errors.New("safetensors file must hold a 2-D F32 tensor named embeddings")
	}
	rows, dim = e.Shape[0], e.Shape[1]
	body := data[8+headerLen:]
	if e.Offsets[0] < 0 || e.Offsets[1] > len(body) || e.Offsets[1]-e.Offsets[0] != rows*dim*4 {
		return nil, 0, 0, errors.New("safetensors tensor data does not match its shape")
	}
	body = body[e.Offsets[0]:e.Offsets[1]]
	table = make([]float32, rows*dim)
	for i := range table {
		table[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[i*4:]))
	}
	return table, rows, dim, nil
}

// NewStaticEmbedder reads an asset made by BuildStaticModel.
func NewStaticEmbedder(asset []byte) (*StaticEmbedder, error) {
	r := &assetReader{data: asset}
	if string(r.take(len(assetMagic))) != assetMagic {
		return nil, errors.New("not an shh embedding model")
	}
	e := &StaticEmbedder{id: r.string()}
	dim, rows, unk, maxChars := int(r.uint32()), int(r.uint32()), int(r.uint32()), int(r.uint32())
	e.dim = dim
	if r.err != nil || dim <= 0 || rows <= 0 || unk >= rows {
		return nil, errors.New("embedding model header is damaged")
	}
	e.tokenizer = wordPiece{vocab: make(map[string]int, rows), unk: unk, maxChars: maxChars}
	for i := 0; i < rows; i++ {
		e.tokenizer.vocab[r.string()] = i
	}
	e.scales = make([]float32, rows)
	e.rows = make([]int8, rows*e.dim)
	for i := 0; i < rows; i++ {
		e.scales[i] = math.Float32frombits(r.uint32())
		for j, b := range r.take(e.dim) {
			e.rows[i*e.dim+j] = int8(b)
		}
	}
	if r.err != nil {
		return nil, fmt.Errorf("embedding model is damaged: %w", r.err)
	}
	if r.pos != len(asset) {
		return nil, errors.New("embedding model has trailing bytes")
	}
	return e, nil
}

type assetReader struct {
	data []byte
	pos  int
	err  error
}

func (r *assetReader) take(n int) []byte {
	if r.err != nil || n < 0 || r.pos+n > len(r.data) {
		r.err = errors.New("unexpected end of file")
		return make([]byte, max(n, 0))
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out
}

func (r *assetReader) uint32() uint32 { return binary.LittleEndian.Uint32(r.take(4)) }

func (r *assetReader) string() string {
	return string(r.take(int(binary.LittleEndian.Uint16(r.take(2)))))
}

func (e *StaticEmbedder) ID() string { return e.id }
func (e *StaticEmbedder) Dim() int   { return e.dim }

// Embed averages the rows of the text's known tokens and scales the result to unit length.
func (e *StaticEmbedder) Embed(text string) []float32 {
	sum := make([]float64, e.dim)
	ids := e.tokenizer.ids(text)
	for _, id := range ids {
		scale := float64(e.scales[id])
		for i, q := range e.rows[id*e.dim : (id+1)*e.dim] {
			sum[i] += scale * float64(q)
		}
	}
	var norm float64
	for _, v := range sum {
		norm += v * v
	}
	out := make([]float32, e.dim)
	if len(ids) == 0 || norm == 0 {
		return out
	}
	norm = math.Sqrt(norm)
	for i, v := range sum {
		out[i] = float32(v / norm)
	}
	return out
}

// ResolveEmbedder finds the embedding model: the copy built into the binary (release builds), else dir/ModelFileName. The file
// must match the expected sha256. With no model it returns ErrNoEmbeddingModel and the classifier runs without layer 2.
func ResolveEmbedder(dir, wantSHA256 string) (Embedder, string, error) {
	if data := embeddedModel(); data != nil {
		e, err := loadVerified(data, wantSHA256)
		if err != nil {
			return nil, "", fmt.Errorf("embedded embedding model: %w", err)
		}
		return e, "embedded", nil
	}
	if dir == "" {
		return nil, "", ErrNoEmbeddingModel
	}
	path := filepath.Join(dir, ModelFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", ErrNoEmbeddingModel
	}
	if err != nil {
		return nil, "", err
	}
	e, err := loadVerified(data, wantSHA256)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return e, path, nil
}

func loadVerified(data []byte, wantSHA256 string) (Embedder, error) {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, wantSHA256) {
		return nil, fmt.Errorf("checksum %s does not match the expected %s (download it again with shh init)", got, wantSHA256)
	}
	e, err := NewStaticEmbedder(data)
	if err != nil {
		return nil, err
	}
	return e, nil
}
