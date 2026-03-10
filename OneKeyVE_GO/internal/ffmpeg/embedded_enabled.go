//go:build bundled_ffmpeg

package ffmpeg

import (
	"embed"
	"io/fs"
	"path/filepath"
)

//go:embed embedded_bins/*
var embeddedRuntimeFS embed.FS

func embeddedPayloads() (map[string][]byte, bool) {
	entries, err := fs.ReadDir(embeddedRuntimeFS, "embedded_bins")
	if err != nil {
		return nil, false
	}

	payloads := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		raw, readErr := embeddedRuntimeFS.ReadFile(filepath.ToSlash(filepath.Join("embedded_bins", name)))
		if readErr != nil {
			return nil, false
		}
		payloads[name] = raw
	}

	if len(payloads) == 0 {
		return nil, false
	}
	return payloads, true
}
