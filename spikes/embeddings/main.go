// Spike 05: load Model2Vec static embeddings in pure Go and sanity-check them on synthetic notifications.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type tokenizer struct {
	vocab    map[string]int
	unk      int
	maxChars int
}

func loadTokenizer(path string) (*tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t struct {
		Model struct {
			Vocab    map[string]int `json:"vocab"`
			Unk      string         `json:"unk_token"`
			MaxChars int            `json:"max_input_chars_per_word"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &tokenizer{vocab: t.Model.Vocab, unk: t.Model.Vocab[t.Model.Unk], maxChars: t.Model.MaxChars}, nil
}

func isChinese(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func isPunct(r rune) bool {
	return (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) || unicode.IsPunct(r)
}

// words mirrors BertNormalizer (clean, chinese chars, lowercase, strip accents) plus BertPreTokenizer.
func words(text string) []string {
	var cleaned strings.Builder
	for _, r := range text {
		switch {
		case r == 0 || r == 0xfffd || (unicode.IsControl(r) && !unicode.IsSpace(r)):
		case unicode.IsSpace(r):
			cleaned.WriteRune(' ')
		case isChinese(r):
			cleaned.WriteString(" " + string(r) + " ")
		default:
			cleaned.WriteRune(r)
		}
	}
	var stripped strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(cleaned.String())) {
		if !unicode.Is(unicode.Mn, r) {
			stripped.WriteRune(r)
		}
	}
	var out []string
	for _, field := range strings.Fields(stripped.String()) {
		var cur []rune
		for _, r := range field {
			if isPunct(r) {
				if len(cur) > 0 {
					out = append(out, string(cur))
					cur = nil
				}
				out = append(out, string(r))
			} else {
				cur = append(cur, r)
			}
		}
		if len(cur) > 0 {
			out = append(out, string(cur))
		}
	}
	return out
}

func (t *tokenizer) wordPiece(word string) []int {
	runes := []rune(word)
	if len(runes) > t.maxChars {
		return []int{t.unk}
	}
	var ids []int
	for start := 0; start < len(runes); {
		end, found := len(runes), -1
		for ; end > start; end-- {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := t.vocab[piece]; ok {
				found = id
				break
			}
		}
		if found < 0 {
			return []int{t.unk}
		}
		ids = append(ids, found)
		start = end
	}
	return ids
}

func (t *tokenizer) encode(text string) []int {
	var ids []int
	for _, w := range words(text) {
		for _, id := range t.wordPiece(w) {
			if id != t.unk {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

type table interface {
	row(id int, buf []float32) []float32
	bytes() int
}

type f32Table struct {
	dim  int
	data []float32
}

func (t f32Table) row(id int, _ []float32) []float32 { return t.data[id*t.dim : (id+1)*t.dim] }
func (t f32Table) bytes() int                        { return len(t.data) * 4 }

type f16Table struct {
	dim  int
	data []uint16
}

func (t f16Table) bytes() int { return len(t.data) * 2 }
func (t f16Table) row(id int, buf []float32) []float32 {
	for i := range buf {
		buf[i] = f16ToF32(t.data[id*t.dim+i])
	}
	return buf
}

type i8Table struct {
	dim    int
	data   []int8
	scales []float32
}

func (t i8Table) bytes() int { return len(t.data) + len(t.scales)*4 }
func (t i8Table) row(id int, buf []float32) []float32 {
	for i := range buf {
		buf[i] = float32(t.data[id*t.dim+i]) * t.scales[id]
	}
	return buf
}

func f32ToF16(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	switch {
	case exp <= 0:
		return sign
	case exp >= 31:
		return sign | 0x7c00
	}
	h := sign | uint16(exp<<10) | uint16(mant>>13)
	if mant&0x1000 != 0 {
		h++
	}
	return h
}

func f16ToF32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	switch exp {
	case 0:
		return math.Float32frombits(sign)
	case 31:
		return math.Float32frombits(sign | 0x7f800000 | mant<<13)
	}
	return math.Float32frombits(sign | (exp+127-15)<<23 | mant<<13)
}

func loadSafetensors(path string) ([]float32, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	headerLen := binary.LittleEndian.Uint64(raw[:8])
	var header map[string]struct {
		DType   string `json:"dtype"`
		Shape   []int  `json:"shape"`
		Offsets [2]int `json:"data_offsets"`
	}
	if err := json.Unmarshal(raw[8:8+headerLen], &header); err != nil {
		return nil, 0, err
	}
	e, ok := header["embeddings"]
	if !ok || e.DType != "F32" || len(e.Shape) != 2 {
		return nil, 0, fmt.Errorf("unexpected tensor layout: %+v", header)
	}
	body := raw[8+int(headerLen)+e.Offsets[0] : 8+int(headerLen)+e.Offsets[1]]
	data := make([]float32, len(body)/4)
	for i := range data {
		data[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[i*4:]))
	}
	return data, e.Shape[1], nil
}

func quantize(data []float32, dim int) (table, table) {
	rows := len(data) / dim
	h := make([]uint16, len(data))
	for i, v := range data {
		h[i] = f32ToF16(v)
	}
	q := make([]int8, len(data))
	scales := make([]float32, rows)
	for r := 0; r < rows; r++ {
		var maxAbs float32
		for _, v := range data[r*dim : (r+1)*dim] {
			maxAbs = max(maxAbs, float32(math.Abs(float64(v))))
		}
		if maxAbs == 0 {
			scales[r] = 1
			continue
		}
		scales[r] = maxAbs / 127
		for i, v := range data[r*dim : (r+1)*dim] {
			q[r*dim+i] = int8(math.Round(float64(v / scales[r])))
		}
	}
	return f16Table{dim, h}, i8Table{dim, q, scales}
}

func embed(tok *tokenizer, tbl table, dim int, text string) []float32 {
	out := make([]float32, dim)
	buf := make([]float32, dim)
	ids := tok.encode(text)
	for _, id := range ids {
		for i, v := range tbl.row(id, buf) {
			out[i] += v
		}
	}
	var norm2 float64
	for i := range out {
		out[i] /= float32(max(len(ids), 1))
		norm2 += float64(out[i] * out[i])
	}
	if norm2 > 0 {
		n := float32(math.Sqrt(norm2))
		for i := range out {
			out[i] /= n
		}
	}
	return out
}

func cos(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i] * b[i])
	}
	return s
}

// logisticLOO trains a tiny L2-regularised logistic regression on all-but-one example and tests on the held-out one.
func logisticLOO(vecs [][]float32, dim int) float64 {
	correct := 0
	for held := range examples {
		w, b := make([]float64, dim), 0.0
		for epoch := 0; epoch < 300; epoch++ {
			for i := range examples {
				if i == held {
					continue
				}
				z := b
				for k := 0; k < dim; k++ {
					z += w[k] * float64(vecs[i][k])
				}
				y := 0.0
				if examples[i].urgent {
					y = 1
				}
				g := 1/(1+math.Exp(-z)) - y
				for k := 0; k < dim; k++ {
					w[k] -= 0.5 * (g*float64(vecs[i][k]) + 0.01*w[k])
				}
				b -= 0.5 * g
			}
		}
		z := b
		for k := 0; k < dim; k++ {
			z += w[k] * float64(vecs[held][k])
		}
		if (z > 0) == examples[held].urgent {
			correct++
		}
	}
	return 100 * float64(correct) / float64(len(examples))
}

type example struct {
	text   string
	urgent bool
}

var examples = []example{
	{"Production server is down, customers cannot log in", true},
	{"URGENT: please approve the payment before 3pm today", true},
	{"Can you jump on a call now? The deploy is failing", true},
	{"Your meeting with the CEO starts in 5 minutes", true},
	{"Alert: database CPU at 98% and rising", true},
	{"Are you available? I need a decision on the contract asap", true},
	{"Security incident: suspicious login detected on your account", true},
	{"@you the build is broken on main, blocking the release", true},
	{"Reminder: board presentation starts in 10 minutes", true},
	{"Outage reported in the payments service, need you in the war room", true},
	{"Direct question: did you push the fix for the checkout bug?", true},
	{"Your flight has been cancelled, rebook immediately", true},
	{"Weekly newsletter: 10 tips to boost your productivity", false},
	{"20% off sale this weekend only, shop now", false},
	{"FYI: team lunch photos are uploaded to the shared folder", false},
	{"A new software update is available for your device", false},
	{"Your monthly statement is ready to view", false},
	{"Someone liked your post on the company intranet", false},
	{"Digest: 14 new comments in channels you follow", false},
	{"Reminder to complete your annual compliance training this quarter", false},
	{"Meet our new hire, welcome Sam to the marketing team", false},
	{"Your weekly screen time report is ready", false},
	{"Webinar replay: future of cloud cost management", false},
	{"No-reply: your package was delivered to the front desk", false},
}

func main() {
	root := "private/models"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if len(os.Args) == 4 {
		dumpVectors(filepath.Join(root, "potion-base-8M"), os.Args[2], os.Args[3])
		return
	}
	fmt.Printf("%-18s %-5s %9s %8s %9s %8s %7s %11s %10s\n", "model", "prec", "size_MB", "load_ms", "us/embed", "centroid", "LR_LOO", "intra-inter", "cos_vs_f32")
	for _, name := range []string{"potion-base-2M", "potion-base-4M", "potion-base-8M"} {
		dir := filepath.Join(root, name)
		tok, err := loadTokenizer(filepath.Join(dir, "tokenizer.json"))
		if err != nil {
			fmt.Println(err)
			return
		}
		t0 := time.Now()
		data, dim, err := loadSafetensors(filepath.Join(dir, "model.safetensors"))
		if err != nil {
			fmt.Println(err)
			return
		}
		loadMs := time.Since(t0).Milliseconds()
		f16, i8 := quantize(data, dim)
		base := f32Table{dim, data}
		var baseVecs [][]float32
		for _, e := range examples {
			baseVecs = append(baseVecs, embed(tok, base, dim, e.text))
		}
		for _, v := range []struct {
			label string
			tbl   table
		}{{"f32", base}, {"f16", f16}, {"int8", i8}} {
			t1 := time.Now()
			vecs := make([][]float32, len(examples))
			for rep := 0; rep < 200; rep++ {
				for i, e := range examples {
					vecs[i] = embed(tok, v.tbl, dim, e.text)
				}
			}
			per := float64(time.Since(t1).Microseconds()) / float64(200*len(examples))
			correct, intra, inter, nIntra, nInter := 0, 0.0, 0.0, 0, 0
			for i := range examples {
				var cu, cn []float32 = make([]float32, dim), make([]float32, dim)
				var nu, nn int
				for j := range examples {
					if j == i {
						continue
					}
					target := cn
					if examples[j].urgent {
						target, nu = cu, nu+1
					} else {
						nn++
					}
					for k := range target {
						target[k] += vecs[j][k]
					}
				}
				su, sn := cos(vecs[i], cu)/float64(nu), cos(vecs[i], cn)/float64(nn)
				if (su > sn) == examples[i].urgent {
					correct++
				}
				for j := range examples {
					if j <= i {
						continue
					}
					c := cos(vecs[i], vecs[j])
					if examples[i].urgent == examples[j].urgent {
						intra, nIntra = intra+c, nIntra+1
					} else {
						inter, nInter = inter+c, nInter+1
					}
				}
			}
			var drift float64
			for i := range vecs {
				drift += cos(vecs[i], baseVecs[i])
			}
			fmt.Printf("%-18s %-5s %9.1f %8d %9.1f %7.0f%% %6.0f%% %11.3f %10.4f\n", name, v.label, float64(v.tbl.bytes())/1e6, loadMs, per,
				100*float64(correct)/float64(len(examples)), logisticLOO(vecs, dim), intra/float64(nIntra)-inter/float64(nInter), drift/float64(len(vecs)))
		}
		if name == "potion-base-8M" {
			for _, probe := range []string{"server is down", "50% off everything", "can you call me right now"} {
				pv := embed(tok, base, dim, probe)
				type hit struct {
					c float64
					t string
				}
				var hits []hit
				for i, e := range examples {
					hits = append(hits, hit{cos(pv, baseVecs[i]), e.text})
				}
				sort.Slice(hits, func(a, b int) bool { return hits[a].c > hits[b].c })
				fmt.Printf("\nprobe %q -> nearest: %.2f %q, %.2f %q\n", probe, hits[0].c, hits[0].t, hits[1].c, hits[1].t)
			}
		}
	}
}

// dumpVectors embeds a JSON list of strings so another implementation can be compared against it.
func dumpVectors(dir, textsPath, outPath string) {
	tok, _ := loadTokenizer(filepath.Join(dir, "tokenizer.json"))
	data, dim, _ := loadSafetensors(filepath.Join(dir, "model.safetensors"))
	raw, _ := os.ReadFile(textsPath)
	var texts []string
	_ = json.Unmarshal(raw, &texts)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = embed(tok, f32Table{dim, data}, dim, t)
	}
	enc, _ := json.Marshal(out)
	_ = os.WriteFile(outPath, enc, 0o600)
}
