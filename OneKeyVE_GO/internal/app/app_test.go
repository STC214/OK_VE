package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOutputDirMakesRelativePathWorkdirRelative(t *testing.T) {
	workDir := filepath.Join("F:\\", "videos")
	got := resolveOutputDir(workDir, "output")
	want := filepath.Join(workDir, "output")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
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

func mustWriteTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
