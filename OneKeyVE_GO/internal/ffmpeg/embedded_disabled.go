//go:build !bundled_ffmpeg

package ffmpeg

func embeddedPayloads() (map[string][]byte, bool) {
	return nil, false
}
