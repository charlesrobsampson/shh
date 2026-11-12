package classify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultModelURL is where the published model asset lives: the models-v1 release (task model:publish). A new model is a new tag, URL and
// checksum together.
const DefaultModelURL = "https://github.com/charlesrobsampson/shh/releases/download/models-v1/" + ModelFileName

// maxModelBytes bounds a download, so a wrong URL cannot fill the disk.
const maxModelBytes = 64 << 20

// DownloadModel fetches the model from url into dir, checks it against the sha256 and only then puts it in place (a partial or wrong file
// is never left where the classifier would load it). client may be nil.
func DownloadModel(ctx context.Context, dir, url, wantSHA256 string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", url, resp.Status)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".model-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxModelBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > maxModelBytes {
		return "", errors.New("the download is larger than any model shh uses")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, wantSHA256) {
		return "", fmt.Errorf("the download has checksum %s, not the expected %s", got, wantSHA256)
	}
	path := filepath.Join(dir, ModelFileName)
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}
