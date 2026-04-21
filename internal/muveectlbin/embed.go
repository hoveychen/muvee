// Package muveectlbin embeds cross-compiled muveectl binaries into the muvee
// server binary so the hub can serve them from /api/muveectl/<asset>. When a
// requested asset is not embedded (local dev builds leave binaries/ empty),
// Open returns fs.ErrNotExist and the caller falls back to GitHub releases.
//
// Binaries live in binaries/ as gzipped files named muveectl_<os>_<arch>[.exe].gz.
// The release workflow populates that directory before building muvee.
package muveectlbin

import (
	"compress/gzip"
	"embed"
	"errors"
	"io"
	"io/fs"
)

//go:embed binaries
var binariesFS embed.FS

// ErrNotEmbedded is returned when the requested asset is not present in the
// embedded filesystem. Callers should treat this as a signal to fall back to
// an external source (e.g. GitHub releases).
var ErrNotEmbedded = errors.New("muveectl asset not embedded")

// Has reports whether the given asset is available in the embedded filesystem.
func Has(asset string) bool {
	_, err := fs.Stat(binariesFS, "binaries/"+asset+".gz")
	return err == nil
}

// Open returns a reader that streams the decompressed binary for the given
// asset. The caller is responsible for closing the reader. Returns
// ErrNotEmbedded when the asset is not present.
func Open(asset string) (io.ReadCloser, error) {
	f, err := binariesFS.Open("binaries/" + asset + ".gz")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotEmbedded
		}
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &gzReadCloser{gz: gz, src: f}, nil
}

type gzReadCloser struct {
	gz  *gzip.Reader
	src io.Closer
}

func (r *gzReadCloser) Read(p []byte) (int, error) { return r.gz.Read(p) }

func (r *gzReadCloser) Close() error {
	gzErr := r.gz.Close()
	srcErr := r.src.Close()
	if gzErr != nil {
		return gzErr
	}
	return srcErr
}
