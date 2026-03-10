package ffmpeg

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"onekeyvego/internal/procutil"
)

var (
	executablePathFunc = os.Executable
	lookPathFunc       = exec.LookPath
	driveRootsFunc     = listDriveRoots
)

type Binaries struct {
	FFmpeg  string
	FFprobe string
	Source  string
}

type VideoMeta struct {
	Width  int
	Height int
	Frames int
}

type diagnostics struct {
	Components map[string]struct {
		Path string `json:"path"`
	} `json:"components"`
}

func Locate(root string, overrides Binaries) (Binaries, error) {
	overrides = mergeEnvOverrides(overrides)
	if overrides.FFmpeg != "" && overrides.FFprobe != "" {
		return Binaries{
			FFmpeg:  overrides.FFmpeg,
			FFprobe: overrides.FFprobe,
			Source:  "flags/env",
		}, nil
	}

	searches := []func(string) (Binaries, error){
		func(scanRoot string) (Binaries, error) { return fromEmbedded(scanRoot) },
		func(_ string) (Binaries, error) { return fromExecutableDir() },
		func(_ string) (Binaries, error) { return fromDriveRoots() },
		func(scanRoot string) (Binaries, error) { return fromFilesystem(scanRoot) },
		func(_ string) (Binaries, error) { return fromPath() },
		func(scanRoot string) (Binaries, error) { return fromDiagnostics(scanRoot) },
	}

	for _, search := range searches {
		bins, err := search(root)
		if err != nil {
			continue
		}
		bins = applyOverrides(bins, overrides)
		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return bins, nil
		}
	}

	return Binaries{}, errors.New("ffmpeg not found in flags/env, embedded payloads, executable directory, drive-root ffmpeg folders, workspace scan, PATH, or diagnostics")
}

func DetectPreferredEncoder(ffmpegPath string, override string) string {
	if override != "" {
		return override
	}

	cmd := exec.Command(ffmpegPath, "-hide_banner", "-encoders")
	procutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "libx264"
	}

	encoders := strings.ToLower(string(out))
	if strings.Contains(encoders, "h264_nvenc") {
		return "h264_nvenc"
	}
	return "libx264"
}

func Probe(ffprobePath string, videoPath string) (VideoMeta, error) {
	cmd := exec.Command(
		ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-count_frames",
		"-show_entries", "stream=width,height,nb_frames,nb_read_frames",
		"-of", "json",
		videoPath,
	)
	procutil.HideWindow(cmd)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return VideoMeta{}, fmt.Errorf("ffprobe %s: %w (%s)", videoPath, err, strings.TrimSpace(stderr.String()))
	}

	var payload struct {
		Streams []struct {
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			Frames       string `json:"nb_frames"`
			ReadFrames   string `json:"nb_read_frames"`
			DisplayRatio string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return VideoMeta{}, fmt.Errorf("parse ffprobe output for %s: %w", videoPath, err)
	}
	if len(payload.Streams) == 0 {
		return VideoMeta{}, fmt.Errorf("ffprobe returned no video streams for %s", videoPath)
	}

	stream := payload.Streams[0]
	if stream.Width == 0 || stream.Height == 0 {
		return VideoMeta{}, fmt.Errorf("invalid dimensions for %s", videoPath)
	}

	return VideoMeta{
		Width:  stream.Width,
		Height: stream.Height,
		Frames: firstPositiveInt(stream.Frames, stream.ReadFrames),
	}, nil
}

func RunWithProgress(cmd *exec.Cmd, totalFrames int, onProgress func(currentFrame int)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open ffmpeg progress pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "frame=") {
			frame, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "frame=")))
			if parseErr == nil && frame >= 0 {
				onProgress(frame)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ffmpeg progress: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg exited with error: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func FindVideos(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	exts := map[string]bool{
		".mp4":  true,
		".mov":  true,
		".mkv":  true,
		".avi":  true,
		".flv":  true,
		".wmv":  true,
		".m4v":  true,
		".webm": true,
	}

	var videos []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if exts[ext] {
			videos = append(videos, filepath.Join(root, entry.Name()))
		}
	}
	return videos, nil
}

func fromDiagnostics(root string) (Binaries, error) {
	var candidates []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), "ffmpeg_full_diagnostics.json") {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return Binaries{}, err
	}

	for _, candidate := range candidates {
		raw, readErr := os.ReadFile(candidate)
		if readErr != nil {
			continue
		}

		var payload diagnostics
		if jsonErr := json.Unmarshal(raw, &payload); jsonErr != nil {
			continue
		}

		ffmpegPath := payload.Components["ffmpeg"].Path
		ffprobePath := payload.Components["ffprobe"].Path
		if fileExists(ffmpegPath) && fileExists(ffprobePath) {
			return Binaries{
				FFmpeg:  ffmpegPath,
				FFprobe: ffprobePath,
				Source:  candidate,
			}, nil
		}
	}

	return Binaries{}, errors.New("no usable diagnostics file found")
}

func fromFilesystem(root string) (Binaries, error) {
	if strings.TrimSpace(root) == "" {
		return Binaries{}, errors.New("workspace scan root is empty")
	}
	wantFFmpeg := binaryName("ffmpeg")
	wantFFprobe := binaryName("ffprobe")

	var bins Binaries
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		name := filepath.Base(path)
		switch {
		case bins.FFmpeg == "" && strings.EqualFold(name, wantFFmpeg):
			bins.FFmpeg = path
		case bins.FFprobe == "" && strings.EqualFold(name, wantFFprobe):
			bins.FFprobe = path
		}

		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return Binaries{}, err
	}
	if bins.FFmpeg == "" || bins.FFprobe == "" {
		return Binaries{}, errors.New("ffmpeg binaries not found in workspace scan")
	}
	bins.Source = root
	return bins, nil
}

func fromExecutableDir() (Binaries, error) {
	executablePath, err := executablePathFunc()
	if err != nil {
		return Binaries{}, err
	}

	executableDir := filepath.Dir(executablePath)
	return fromCandidateDirectories([]candidateDir{
		{Path: filepath.Join(executableDir, "ffmpeg", "bin"), Source: filepath.Join(executableDir, "ffmpeg", "bin")},
		{Path: filepath.Join(executableDir, "ffmpeg"), Source: filepath.Join(executableDir, "ffmpeg")},
		{Path: executableDir, Source: executableDir},
	})
}

func fromDriveRoots() (Binaries, error) {
	roots, err := driveRootsFunc()
	if err != nil {
		return Binaries{}, err
	}

	var candidates []candidateDir
	for _, root := range roots {
		candidates = append(candidates,
			candidateDir{
				Path:   filepath.Join(root, "ffmpeg", "bin"),
				Source: filepath.Join(root, "ffmpeg", "bin"),
			},
			candidateDir{
				Path:   filepath.Join(root, "ffmpeg"),
				Source: filepath.Join(root, "ffmpeg"),
			},
		)
	}

	return fromCandidateDirectories(candidates)
}

func fromPath() (Binaries, error) {
	ffmpegPath, err := lookPathFunc(binaryName("ffmpeg"))
	if err != nil {
		return Binaries{}, err
	}
	ffprobePath, err := lookPathFunc(binaryName("ffprobe"))
	if err != nil {
		return Binaries{}, err
	}

	return Binaries{
		FFmpeg:  ffmpegPath,
		FFprobe: ffprobePath,
		Source:  "PATH",
	}, nil
}

func fromEmbedded(root string) (Binaries, error) {
	payloads, ok := embeddedPayloads()
	if !ok {
		return Binaries{}, errors.New("no embedded ffmpeg payloads available")
	}

	runtimeDir, err := chooseRuntimeDir(root)
	if err != nil {
		return Binaries{}, err
	}
	binDir := filepath.Join(runtimeDir, "embedded-ffmpeg", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return Binaries{}, fmt.Errorf("create embedded ffmpeg directory: %w", err)
	}

	if err := writeEmbeddedRuntime(binDir, payloads); err != nil {
		return Binaries{}, err
	}

	ffmpegPath := filepath.Join(binDir, binaryName("ffmpeg"))
	ffprobePath := filepath.Join(binDir, binaryName("ffprobe"))
	if !fileExists(ffmpegPath) || !fileExists(ffprobePath) {
		return Binaries{}, errors.New("embedded runtime missing ffmpeg.exe or ffprobe.exe")
	}

	return Binaries{
		FFmpeg:  ffmpegPath,
		FFprobe: ffprobePath,
		Source:  "embedded",
	}, nil
}

type candidateDir struct {
	Path   string
	Source string
}

func fromCandidateDirectories(candidates []candidateDir) (Binaries, error) {
	for _, candidate := range candidates {
		bins := binariesFromDirectory(candidate.Path, candidate.Source)
		if bins.FFmpeg != "" && bins.FFprobe != "" {
			return bins, nil
		}
	}
	return Binaries{}, errors.New("ffmpeg binaries not found in candidate directories")
}

func binariesFromDirectory(dir string, source string) Binaries {
	if strings.TrimSpace(dir) == "" {
		return Binaries{}
	}

	ffmpegPath := filepath.Join(dir, binaryName("ffmpeg"))
	ffprobePath := filepath.Join(dir, binaryName("ffprobe"))
	if fileExists(ffmpegPath) && fileExists(ffprobePath) {
		return Binaries{
			FFmpeg:  ffmpegPath,
			FFprobe: ffprobePath,
			Source:  source,
		}
	}
	return Binaries{}
}

func binaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstPositiveInt(values ...string) int {
	for _, value := range values {
		if value == "" || strings.EqualFold(value, "N/A") {
			continue
		}
		n, err := strconv.Atoi(value)
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func mergeEnvOverrides(overrides Binaries) Binaries {
	if overrides.FFmpeg == "" {
		overrides.FFmpeg = firstNonEmpty(os.Getenv("ONEKEYVE_FFMPEG"), os.Getenv("FFMPEG_PATH"))
	}
	if overrides.FFprobe == "" {
		overrides.FFprobe = firstNonEmpty(os.Getenv("ONEKEYVE_FFPROBE"), os.Getenv("FFPROBE_PATH"))
	}
	return overrides
}

func applyOverrides(bins Binaries, overrides Binaries) Binaries {
	usedOverride := false
	if overrides.FFmpeg != "" {
		bins.FFmpeg = overrides.FFmpeg
		usedOverride = true
	}
	if overrides.FFprobe != "" {
		bins.FFprobe = overrides.FFprobe
		usedOverride = true
	}
	if usedOverride && bins.Source != "" && bins.Source != "flags/env" {
		bins.Source += " + flags/env"
	}
	return bins
}

func listDriveRoots() ([]string, error) {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}, nil
	}

	var roots []string
	for drive := 'A'; drive <= 'Z'; drive++ {
		root := fmt.Sprintf("%c:\\", drive)
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("no drive roots found")
	}
	return roots, nil
}

func chooseRuntimeDir(root string) (string, error) {
	for _, candidate := range runtimeBaseCandidates(root) {
		if candidate == "" {
			continue
		}
		if isForbiddenOutputPath(candidate) {
			continue
		}
		return filepath.Join(candidate, "OneKeyVE"), nil
	}
	return "", errors.New("no writable non-C runtime directory available for embedded ffmpeg")
}

func runtimeBaseCandidates(root string) []string {
	candidates := []string{
		os.Getenv("ONEKEYVE_RUNTIME_DIR"),
		os.Getenv("ONEKEYVE_CACHE_DIR"),
		os.Getenv("TMPDIR"),
		os.Getenv("TEMP"),
		os.Getenv("TMP"),
	}

	if executablePath, err := executablePathFunc(); err == nil {
		candidates = append(candidates, filepath.Dir(executablePath))
	}
	if filepath.IsAbs(root) {
		candidates = append(candidates, root)
	}
	return candidates
}

func writePayloadIfDifferent(path string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("embedded payload missing for %s", path)
	}

	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, payload) {
		return nil
	}
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		return fmt.Errorf("write embedded payload %s: %w", path, err)
	}
	return nil
}

func writeEmbeddedRuntime(binDir string, payloads map[string][]byte) error {
	for name, payload := range payloads {
		if err := writePayloadIfDifferent(filepath.Join(binDir, name), payload); err != nil {
			return err
		}
	}
	return nil
}

func isForbiddenOutputPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return true
	}
	return strings.EqualFold(filepath.VolumeName(clean), "C:")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
