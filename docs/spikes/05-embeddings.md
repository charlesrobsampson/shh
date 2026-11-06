# Spike 05: static embedding model choice

Question: which small static embedding model do we bundle with `go:embed` for classifier layer L2, and how do we tokenize in pure Go? (`plan.md` §5.1)

Candidates are the Model2Vec "potion" family from `minishlab` on Hugging Face (all MIT). Files are downloaded by `task model:fetch` into the gitignored `private/models/`; nothing large is committed. Prototype: `spikes/embeddings/` (own Go module; `task spike:embeddings`).

## Candidates

| Model | License | Dim | Vocab | `model.safetensors` (F32) | MTEB avg | MTEB classification |
|---|---|---|---|---|---|---|
| potion-base-2M | MIT | 64 | 29,528 | 7.6 MB | 47.49 | 64.13 |
| potion-base-4M | MIT | 128 | 29,528 | 15.1 MB | 49.77 | 68.00 |
| potion-base-8M | MIT | 256 | 29,528 | 30.2 MB | 51.08 | 70.34 |

Reference points from the same model cards: all-MiniLM-L6-v2 (transformer) 55.93 / 69.25, GloVe 300d 45.82 / 62.73. potion-retrieval-32M is 129 MB with 512 dims, too big. Scores are the authors' published MTEB numbers, not our own measurements.

## Format and tokenizer

- All three share one tokenizer: BERT WordPiece (`bge-base-en-v1.5`): normalizer = clean text, lowercase, strip accents, space out CJK characters; pre-tokenizer = split on whitespace and punctuation; WordPiece with `##` continuation, 100 chars max per word, `[UNK]` for unknown words. The vocab has 29,528 entries and the ids are exactly the row numbers of the embedding matrix, so no id mapping is needed.
- Weights: one safetensors tensor `embeddings`, shape `[29528, dim]`, dtype F32. Trivial to parse in Go (8-byte header length, JSON header, raw little-endian floats).
- Inference: encode, drop `[UNK]`, mean of the rows, L2 normalize. The special `[CLS]`/`[SEP]` tokens are not used.
- The Go tokenizer needs `golang.org/x/text/unicode/norm` for accent stripping (pure Go, no CGo). About 150 lines in total.

## Results (`spikes/embeddings`, 24 synthetic notifications, pure Go, CGo off)

| Model | Precision | Stored size | Load | Embed time per notification |
|---|---|---|---|---|
| potion-base-2M | f32 / f16 / int8 | 7.6 / 3.8 / **2.0** MB | 3 ms | about 3 µs |
| potion-base-4M | f32 / f16 / int8 | 15.1 / 7.6 / **3.9** MB | 4 ms | about 3 µs |
| potion-base-8M | f32 / f16 / int8 | 30.2 / 15.1 / **7.7** MB | 8 ms | about 3 to 4 µs |

- **Quantization is effectively lossless:** f16 and per-row-scaled int8 give cosine 1.0000 to the f32 embeddings on all examples and identical classification results.
- **Parity with the reference implementation:** the Go tokenizer and pooling give cosine 1.0000 to the Python `model2vec` library on 13 tricky inputs (accents, Chinese, emoji, URL, punctuation, contractions, empty and whitespace-only strings).
- Nearest-example sanity checks (8M): "server is down" is closest to "Production server is down, customers cannot log in" (0.65); "50% off everything" to "20% off sale this weekend only, shop now" (0.43); "can you call me right now" to the two call/approve requests (0.36 to 0.38).
- Nearest-centroid accuracy was 79% for all three models on the 24 examples and the intra-class minus inter-class cosine gap was small (0.04 to 0.05). A leave-one-out logistic regression scored 67%, 62% and 54% for 2M, 4M and 8M, but that is an artifact: 23 training points cannot fit 256 dimensions, so it says nothing about quality. **Our own examples are too few to rank the models; use the published benchmark.**
- Cold-start note: with a small seed set, higher-dimensional features overfit faster, so the classifier needs L2 regularisation and a decent seed set (task 21). If that proves to be a problem, potion-base-4M (128 dims) is the fallback.

## Recommendation

**potion-base-8M, int8 quantized: 7.7 MB** of weights plus the tokenizer vocabulary (the vocab as a plain token list is about 0.25 MB), about 8 MB added to the binary.

- Best published classification score (70.3, close to MiniLM's 69.3) at an affordable size; 4M int8 would save 3.8 MB for about 2 classification points; 2M (64.1) is clearly weaker.
- Load time (about 10 ms) and embed time (a few microseconds) are negligible.
- The Go tokenizer matches the reference exactly, so no Python is needed at runtime.

### Distribution (decided by the user)

Not committed to git. `go install github.com/charlesrobsampson/shh@latest` (or any version) builds from the module source, where a gitignored file does not exist, so a plain `go:embed` would fail to compile. So:

- The quantized int8 model is published once as a GitHub release asset under a stable tag (`models-v1`); its URL and sha256 are constants in the source. No quantizing on user machines, no dependence on Hugging Face `main`.
- Release builds (`task release`, `-tags embedmodel`) embed it and work offline.
- `go install` builds do not embed; `shh init` downloads and verifies it into `~/.config/shh/models/`. Until then L2 is off and `shh doctor` reports it.
- A future model gets a new `models-vN` tag and constants; the feature version includes the model id, so old weights are invalidated.

## Feature version

The feature version string must include the model id and quantization (`potion-base-8M-int8`), per `plan.md` §5.1.
