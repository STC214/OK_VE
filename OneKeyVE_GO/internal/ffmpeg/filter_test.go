package ffmpeg

import (
	"strings"
	"testing"
)

func TestPlannedDimensions(t *testing.T) {
	rotate, w, h := PlannedDimensions(1920, 1080)
	if !rotate || w != 1080 || h != 1920 {
		t.Fatalf("unexpected landscape normalization: rotate=%v w=%d h=%d", rotate, w, h)
	}

	rotate, w, h = PlannedDimensions(720, 1280)
	if rotate || w != 720 || h != 1280 {
		t.Fatalf("unexpected portrait normalization: rotate=%v w=%d h=%d", rotate, w, h)
	}
}

func TestBuildFilter(t *testing.T) {
	filter := BuildFilter(true, 1081, 1921, CropRect{X: 10, Y: 20, Width: 1080, Height: 1920}, 2401, 20, 30)
	expectedParts := []string{
		"crop=1080:1920:10:20",
		"transpose=1",
		"scale=1080:2400:force_original_aspect_ratio=increase",
		"gblur=sigma=20",
		"drawbox=x=0:y=0:w=1080:h=30:t=fill:c=black",
		"overlay=x=0:y=240:shortest=1:format=auto",
	}

	for _, part := range expectedParts {
		if !strings.Contains(filter, part) {
			t.Fatalf("expected filter to contain %q, got %s", part, filter)
		}
	}
}

func TestBuildFilterCropsBeforeSplitSoBackgroundUsesCroppedSource(t *testing.T) {
	filter := BuildFilter(false, 1080, 1920, CropRect{X: 12, Y: 24, Width: 1000, Height: 1800}, 2400, 20, 30)

	cropIndex := strings.Index(filter, "[0:v]crop=1000:1800:12:24,")
	splitIndex := strings.Index(filter, "[raw]split=2[bg_src][fg_src];")
	bgScaleIndex := strings.Index(filter, "[bg_src]scale=")

	if cropIndex < 0 {
		t.Fatalf("expected source crop in filter, got %s", filter)
	}
	if splitIndex < 0 || bgScaleIndex < 0 {
		t.Fatalf("expected split and background scale stages in filter, got %s", filter)
	}
	if cropIndex > splitIndex {
		t.Fatalf("expected crop to happen before split, got %s", filter)
	}
	if splitIndex > bgScaleIndex {
		t.Fatalf("expected background to be built from split output, got %s", filter)
	}
}

func TestBuildFilterWithoutCrop(t *testing.T) {
	filter := BuildFilter(false, 720, 1280, CropRect{}, 1600, 20, 30)
	if strings.Contains(filter, "[0:v]crop=") {
		t.Fatalf("expected filter without source crop prefix, got %s", filter)
	}
}
