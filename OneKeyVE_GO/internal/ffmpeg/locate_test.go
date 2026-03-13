package ffmpeg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocatePrefersExecutableDirectoryFFmpegFolder(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	exeDir := filepath.Join(tempDir, "app")
	ffmpegDir := filepath.Join(exeDir, "ffmpeg", "bin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(exeDir, "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return nil, nil
	}

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != filepath.Join(exeDir, "ffmpeg", "bin") {
		t.Fatalf("expected executable directory source, got %q", bins.Source)
	}
}

func TestLocateIncludesDriveRootFFmpegFolder(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	driveRoot := filepath.Join(tempDir, "driveC")
	ffmpegDir := filepath.Join(driveRoot, "ffmpeg", "bin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(ffmpegDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(tempDir, "missing", "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return []string{driveRoot}, nil
	}

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != filepath.Join(driveRoot, "ffmpeg", "bin") {
		t.Fatalf("expected drive-root ffmpeg source, got %q", bins.Source)
	}
}

func TestLocateIncludesSystemPath(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	pathDir := filepath.Join(tempDir, "pathbin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(tempDir, "missing", "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return nil, nil
	}

	t.Setenv("PATH", pathDir)

	bins, err := Locate(workDir, Binaries{})
	if err != nil {
		t.Fatalf("Locate returned error: %v", err)
	}
	if bins.Source != "PATH" {
		t.Fatalf("expected PATH source, got %q", bins.Source)
	}
}

func TestChooseRuntimeDirRejectsCAndUsesEnv(t *testing.T) {
	t.Setenv("ONEKEYVE_RUNTIME_DIR", `C:\temp\blocked`)
	t.Setenv("ONEKEYVE_CACHE_DIR", `F:\runtime-ok`)
	t.Setenv("TMPDIR", "")
	t.Setenv("TEMP", "")
	t.Setenv("TMP", "")

	restorePrepareRuntimeDir := prepareRuntimeDir
	t.Cleanup(func() {
		prepareRuntimeDir = restorePrepareRuntimeDir
	})
	prepareRuntimeDir = func(path string) error {
		return nil
	}

	dir, err := chooseRuntimeDir(`F:\work`)
	if err != nil {
		t.Fatalf("chooseRuntimeDir returned error: %v", err)
	}
	expected := filepath.Join(`F:\runtime-ok`, "OneKeyVE")
	if dir != expected {
		t.Fatalf("expected %q, got %q", expected, dir)
	}
}

func TestFindVideosRecursivelyDiscoversNestedFiles(t *testing.T) {
	root := t.TempDir()
	rootVideo := filepath.Join(root, "root.mp4")
	nestedVideo := filepath.Join(root, "nested", "child.mkv")
	ignored := filepath.Join(root, "nested", "note.txt")

	mustWriteFile(t, rootVideo)
	mustWriteFile(t, nestedVideo)
	mustWriteFile(t, ignored)

	videos, err := FindVideos(root)
	if err != nil {
		t.Fatalf("FindVideos returned error: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("expected 2 videos, got %d: %v", len(videos), videos)
	}
	if videos[0] != filepath.Clean(nestedVideo) && videos[1] != filepath.Clean(nestedVideo) {
		t.Fatalf("expected nested video in results, got %v", videos)
	}
	if videos[0] != filepath.Clean(rootVideo) && videos[1] != filepath.Clean(rootVideo) {
		t.Fatalf("expected root video in results, got %v", videos)
	}
}

func TestChooseRuntimeDirFallsBackWhenFirstCandidateIsNotWritable(t *testing.T) {
	t.Setenv("ONEKEYVE_RUNTIME_DIR", `F:\blocked`)
	t.Setenv("ONEKEYVE_CACHE_DIR", `F:\runtime-ok`)
	t.Setenv("TMPDIR", "")
	t.Setenv("TEMP", "")
	t.Setenv("TMP", "")

	restorePrepareRuntimeDir := prepareRuntimeDir
	t.Cleanup(func() {
		prepareRuntimeDir = restorePrepareRuntimeDir
	})

	attempts := make([]string, 0, 2)
	prepareRuntimeDir = func(path string) error {
		attempts = append(attempts, path)
		if path == filepath.Join(`F:\blocked`, "OneKeyVE") {
			return os.ErrPermission
		}
		return nil
	}

	dir, err := chooseRuntimeDir(`F:\work`)
	if err != nil {
		t.Fatalf("chooseRuntimeDir returned error: %v", err)
	}

	want := filepath.Join(`F:\runtime-ok`, "OneKeyVE")
	if dir != want {
		t.Fatalf("expected fallback runtime dir %q, got %q", want, dir)
	}
	if len(attempts) < 2 {
		t.Fatalf("expected multiple runtime dir attempts, got %v", attempts)
	}
}

func TestLocateRejectsInvalidExplicitOverrides(t *testing.T) {
	tempDir := t.TempDir()

	_, err := Locate(tempDir, Binaries{
		FFmpeg:  filepath.Join(tempDir, "missing-ffmpeg.exe"),
		FFprobe: filepath.Join(tempDir, "missing-ffprobe.exe"),
	})
	if err == nil {
		t.Fatalf("expected invalid explicit overrides to fail")
	}
}

func TestLocateRejectsInvalidPartialOverrideInsteadOfMaskingFoundBinary(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "work")
	pathDir := filepath.Join(tempDir, "pathbin")

	mustMkdir(t, workDir)
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffmpeg")))
	mustWriteFile(t, filepath.Join(pathDir, binaryName("ffprobe")))

	restoreExecutable := executablePathFunc
	restoreDriveRoots := driveRootsFunc
	t.Cleanup(func() {
		executablePathFunc = restoreExecutable
		driveRootsFunc = restoreDriveRoots
	})

	executablePathFunc = func() (string, error) {
		return filepath.Join(tempDir, "missing", "OneKeyVE.exe"), nil
	}
	driveRootsFunc = func() ([]string, error) {
		return nil, nil
	}

	t.Setenv("PATH", pathDir)

	_, err := Locate(workDir, Binaries{
		FFmpeg: filepath.Join(tempDir, "does-not-exist", binaryName("ffmpeg")),
	})
	if err == nil {
		t.Fatalf("expected invalid partial override to fail")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
