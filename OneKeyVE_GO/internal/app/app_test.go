package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onekeyvego/internal/ffmpeg"
)

func TestResolveOutputDirMakesRelativePathWorkdirRelative(t *testing.T) {
	workDir := filepath.Join("F:\\", "videos")
	got := resolveOutputDir(workDir, "output")
	want := filepath.Join(workDir, "output")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDefaultTargetsUseUserFacingLabelsAndFixed90Size(t *testing.T) {
	cfg := DefaultConfig(filepath.Join("F:\\", "videos"))
	if len(cfg.Targets) != 3 {
		t.Fatalf("expected 3 default targets, got %d", len(cfg.Targets))
	}
	if cfg.Targets[0].Label != "70Pro" || cfg.Targets[1].Label != "Ace5" || cfg.Targets[2].Label != "90" {
		t.Fatalf("unexpected default targets: %+v", cfg.Targets)
	}

	width, height := cfg.Targets[2].Dimensions(1080)
	if width != 1156 || height != 2510 {
		t.Fatalf("expected 90 target to use 1156x2510, got %dx%d", width, height)
	}
}

func TestResolveOutputDirPreservesAbsolutePath(t *testing.T) {
	workDir := filepath.Join("F:\\", "videos")
	absoluteOutput := filepath.Join("G:\\", "renders")
	got := resolveOutputDir(workDir, absoluteOutput)
	if got != absoluteOutput {
		t.Fatalf("expected absolute output %q to be preserved, got %q", absoluteOutput, got)
	}
}

func TestIsCDrivePathRejectsNormalizedCDriveOutputs(t *testing.T) {
	if !isCDrivePath(filepath.Join("C:", "renders", "..", "renders", "clip.mp4")) {
		t.Fatalf("expected C drive path to be rejected")
	}
	if isCDrivePath(filepath.Join("F:", "renders", "clip.mp4")) {
		t.Fatalf("expected non-C drive path to be allowed")
	}
}

func TestResolveVideoOutputDirUsesConfiguredOutputForRootVideos(t *testing.T) {
	cfg := Config{
		WorkDir:   filepath.Join("F:\\", "videos"),
		OutputDir: filepath.Join("G:\\", "renders"),
	}
	videoPath := filepath.Join(cfg.WorkDir, "clip.mp4")

	got := resolveVideoOutputDir(cfg, videoPath, "9x20")
	want := filepath.Join(cfg.OutputDir, "9x20")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveVideoOutputDirUsesSourceDirectoryForNestedVideos(t *testing.T) {
	cfg := Config{
		WorkDir:   filepath.Join("F:\\", "videos"),
		OutputDir: filepath.Join("G:\\", "renders"),
	}
	videoPath := filepath.Join(cfg.WorkDir, "nested", "clip.mp4")

	got := resolveVideoOutputDir(cfg, videoPath, "9x20")
	want := filepath.Join(cfg.WorkDir, "nested", "9x20")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDiscoverVideosSkipsGeneratedOutputsButKeepsNestedSources(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	rootVideo := filepath.Join(workDir, "root.mp4")
	nestedVideo := filepath.Join(workDir, "nested", "child.mp4")
	rootRendered := filepath.Join(outputDir, "9x20", "root.mp4")
	nestedRendered := filepath.Join(workDir, "nested", "9x20", "child.mp4")

	mustWriteTestFile(t, rootVideo)
	mustWriteTestFile(t, nestedVideo)
	mustWriteTestFile(t, rootRendered)
	mustWriteTestFile(t, nestedRendered)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: outputDir,
		Targets: []Target{
			{Label: "9x20"},
			{Label: "5x11"},
		},
	}

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("expected 2 source videos, got %d: %v", len(videos), videos)
	}
	if videos[0] != rootVideo && videos[1] != rootVideo {
		t.Fatalf("expected root source video to be discovered, got %v", videos)
	}
	if videos[0] != nestedVideo && videos[1] != nestedVideo {
		t.Fatalf("expected nested source video to be discovered, got %v", videos)
	}
}

func TestDiscoverVideosKeepsNestedSourcesWhenRootHasNoVideos(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	nestedVideo := filepath.Join(workDir, "nested", "child.mp4")
	nestedRendered := filepath.Join(workDir, "nested", "9x20", "child.mp4")

	mustWriteTestFile(t, nestedVideo)
	mustWriteTestFile(t, nestedRendered)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: outputDir,
		Targets: []Target{
			{Label: "9x20"},
			{Label: "5x11"},
		},
	}

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("expected 1 nested source video, got %d: %v", len(videos), videos)
	}
	if videos[0] != nestedVideo {
		t.Fatalf("expected nested source video %q, got %v", nestedVideo, videos)
	}
}

func TestDiscoverVideosDoesNotSkipUserVideosInsideTargetNamedFolders(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	targetNamedSource := filepath.Join(workDir, "archive", "9x20", "clip.mp4")

	mustWriteTestFile(t, targetNamedSource)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: outputDir,
		Targets: []Target{
			{Label: "9x20"},
			{Label: "5x11"},
		},
	}

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("expected target-named source folder to be preserved, got %d: %v", len(videos), videos)
	}
	if videos[0] != targetNamedSource {
		t.Fatalf("expected source video %q, got %v", targetNamedSource, videos)
	}
}

func TestDiscoverVideosKeepsNestedSourcesWhenOutputDirEqualsWorkDir(t *testing.T) {
	workDir := t.TempDir()
	nestedVideo := filepath.Join(workDir, "nested", "child.mp4")
	rootRendered := filepath.Join(workDir, "9x20", "root.mp4")

	mustWriteTestFile(t, nestedVideo)
	mustWriteTestFile(t, rootRendered)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: workDir,
		Targets: []Target{
			{Label: "9x20"},
			{Label: "5x11"},
		},
	}

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("expected only nested source video to remain, got %d: %v", len(videos), videos)
	}
	if videos[0] != nestedVideo {
		t.Fatalf("expected nested source video %q, got %v", nestedVideo, videos)
	}
}

func TestDiscoverVideosSkipsLegacyGeneratedOutputFoldersAfterRename(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	rootVideo := filepath.Join(workDir, "root.mp4")
	legacyRendered := filepath.Join(outputDir, "9x20", "root.mp4")

	mustWriteTestFile(t, rootVideo)
	mustWriteTestFile(t, legacyRendered)

	cfg := DefaultConfig(workDir)
	cfg.OutputDir = outputDir

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 1 || videos[0] != rootVideo {
		t.Fatalf("expected only root source video, got %v", videos)
	}
}

func TestDiscoverVideosSkipsUnselectedGeneratedOutputFolders(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	rootVideo := filepath.Join(workDir, "root.mp4")
	unselectedRendered := filepath.Join(outputDir, "Ace5", "root.mp4")

	mustWriteTestFile(t, rootVideo)
	mustWriteTestFile(t, unselectedRendered)

	cfg := DefaultConfig(workDir)
	cfg.OutputDir = outputDir
	cfg.Targets = []Target{
		{Label: "70Pro", Ratio: 9.0 / 20.0, Aliases: []string{"9x20"}},
	}

	videos, err := DiscoverVideos(cfg)
	if err != nil {
		t.Fatalf("DiscoverVideos returned error: %v", err)
	}
	if len(videos) != 1 || videos[0] != rootVideo {
		t.Fatalf("expected only root source video, got %v", videos)
	}
}

func TestPlanTargetRendersSkipsExistingOutputsThatPassProbeValidation(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	videoPath := filepath.Join(workDir, "clip.mp4")
	existingOutput := filepath.Join(outputDir, "9x20", "clip.mp4")
	fakeFFprobe := writeFakeFFprobe(t, true)

	mustWriteSizedTestFile(t, videoPath, 10)
	mustWriteSizedTestFile(t, existingOutput, 5)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: outputDir,
		Targets: []Target{
			{Label: "9x20"},
			{Label: "5x11"},
		},
	}

	plans, err := planTargetRenders(cfg, ffmpeg.Binaries{FFprobe: fakeFFprobe}, videoPath, nil)
	if err != nil {
		t.Fatalf("planTargetRenders returned error: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if !plans[0].SkipExisting {
		t.Fatalf("expected first target to be skipped when existing output probes successfully")
	}
	if plans[0].RemovedFailedExisting {
		t.Fatalf("did not expect valid output to be deleted")
	}
	if plans[1].SkipExisting {
		t.Fatalf("did not expect missing target output to be skipped")
	}
}

func TestPlanTargetRendersDeletesInvalidExistingOutput(t *testing.T) {
	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	videoPath := filepath.Join(workDir, "clip.mp4")
	failedOutput := filepath.Join(outputDir, "9x20", "clip.mp4")
	fakeFFprobe := writeFakeFFprobe(t, false)

	mustWriteSizedTestFile(t, videoPath, 10)
	mustWriteSizedTestFile(t, failedOutput, 20)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: outputDir,
		Targets: []Target{
			{Label: "9x20"},
		},
	}

	plans, err := planTargetRenders(cfg, ffmpeg.Binaries{FFprobe: fakeFFprobe}, videoPath, nil)
	if err != nil {
		t.Fatalf("planTargetRenders returned error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].SkipExisting {
		t.Fatalf("expected invalid output to be re-rendered")
	}
	if !plans[0].RemovedFailedExisting {
		t.Fatalf("expected invalid output to be deleted before re-render")
	}
	if _, err := os.Stat(failedOutput); !os.IsNotExist(err) {
		t.Fatalf("expected invalid output to be removed, got err=%v", err)
	}
}

func TestPlanTargetRendersRejectsActualCDriveOutputDirectories(t *testing.T) {
	workDir := t.TempDir()
	videoPath := filepath.Join(workDir, "clip.mp4")
	mustWriteTestFile(t, videoPath)

	cfg := Config{
		WorkDir:   workDir,
		OutputDir: filepath.Join("C:\\", "renders"),
		Targets: []Target{
			{Label: "9x20"},
		},
	}

	_, err := planTargetRenders(cfg, ffmpeg.Binaries{}, videoPath, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to write output under C drive") {
		t.Fatalf("expected C drive output rejection, got %v", err)
	}
}

func TestTargetVideoBitrateUsesOnePointFiveXSource(t *testing.T) {
	const sourceBitrate = int64(2_000_000)
	got := targetVideoBitrate(sourceBitrate)
	const want = int64(3_000_000)
	if got != want {
		t.Fatalf("expected target bitrate %d, got %d", want, got)
	}
}

func TestBuildCommandUsesDerivedBitrateWhenAvailable(t *testing.T) {
	cmd := buildCommand("ffmpeg.exe", "libx264", "input.mp4", "output.mp4", "scale=720:1280", 3_000_000)
	args := strings.Join(cmd.Args, " ")

	for _, needle := range []string{
		"-b:v 3000000",
		"-maxrate:v 3000000",
		"-bufsize:v 6000000",
	} {
		if !strings.Contains(args, needle) {
			t.Fatalf("expected command args to contain %q, got %s", needle, args)
		}
	}
	if strings.Contains(args, "-crf 20") {
		t.Fatalf("did not expect fixed CRF fallback when bitrate is available, got %s", args)
	}
}

func TestBuildCommandFallsBackWhenBitrateUnavailable(t *testing.T) {
	cmd := buildCommand("ffmpeg.exe", "libx264", "input.mp4", "output.mp4", "scale=720:1280", 0)
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-crf 20") {
		t.Fatalf("expected fallback CRF args when bitrate is unavailable, got %s", args)
	}
}

func mustWriteTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustWriteSizedTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if size < 0 {
		t.Fatalf("invalid file size %d", size)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeFakeFFprobe(t *testing.T, valid bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffprobe.cmd")
	body := "@echo off\r\n"
	if valid {
		body += "echo {\"streams\":[{\"width\":720,\"height\":1280,\"nb_read_frames\":\"10\",\"bit_rate\":\"1000\"}],\"format\":{\"bit_rate\":\"1000\"}}\r\n"
		body += "exit /b 0\r\n"
	} else {
		body += "echo invalid probe >&2\r\n"
		body += "exit /b 1\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	return path
}
