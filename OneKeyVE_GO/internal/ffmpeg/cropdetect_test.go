package ffmpeg

import "testing"

var defaultBlackBorderOptions = normalizeBlackBorderOptions(BlackBorderOptions{})

func TestDetectFrameBordersFindsCenterOutBlackLines(t *testing.T) {
	width := 80
	height := 40
	frame := make([]byte, width*height)
	fillFrameRect(frame, width, 6, 8, 68, 24, 180)

	for y := 2; y < 8; y++ {
		for x := 4; x < 28; x++ {
			frame[y*width+x] = 255
		}
	}
	for y := 12; y < 30; y++ {
		for x := 66; x < 74; x++ {
			frame[y*width+x] = 200
		}
	}

	top, bottom, left, right, ok := detectFrameBorders(frame, width, height, defaultBlackBorderOptions)
	if !ok {
		t.Fatalf("expected border detection to succeed")
	}
	if top != 8 || bottom != 8 || left != 6 || right != 6 {
		t.Fatalf("unexpected borders top=%d bottom=%d left=%d right=%d", top, bottom, left, right)
	}
}

func TestDetectFrameBordersSupportsSingleAxisCrop(t *testing.T) {
	width := 80
	height := 48
	frame := make([]byte, width*height)
	fillFrameRect(frame, width, 10, 0, 60, 48, 140)

	top, bottom, left, right, ok := detectFrameBorders(frame, width, height, defaultBlackBorderOptions)
	if !ok {
		t.Fatalf("expected border detection to succeed")
	}
	if top != 0 || bottom != 0 || left != 10 || right != 10 {
		t.Fatalf("unexpected borders top=%d bottom=%d left=%d right=%d", top, bottom, left, right)
	}
}

func TestDetectFrameBordersHandlesWatermarkCrossingBlackEdge(t *testing.T) {
	width := 96
	height := 64
	frame := make([]byte, width*height)
	fillFrameRect(frame, width, 12, 10, 72, 44, 170)

	for y := 16; y < 36; y++ {
		for x := 8; x < 16; x++ {
			frame[y*width+x] = 230
		}
	}

	top, bottom, left, right, ok := detectFrameBorders(frame, width, height, defaultBlackBorderOptions)
	if !ok {
		t.Fatalf("expected border detection to succeed")
	}
	if top != 10 || bottom != 10 || left != 8 || right != 12 {
		t.Fatalf("unexpected borders top=%d bottom=%d left=%d right=%d", top, bottom, left, right)
	}
}

func TestFrameHasBlackBorder(t *testing.T) {
	noBorder := make([]byte, 40*24)
	for i := range noBorder {
		noBorder[i] = 120
	}
	if frameHasBlackBorder(noBorder, 40, 24) {
		t.Fatalf("expected frame without black border to be ignored")
	}

	withBorder := make([]byte, 40*24)
	fillFrameRect(withBorder, 40, 6, 4, 28, 16, 120)
	if !frameHasBlackBorder(withBorder, 40, 24) {
		t.Fatalf("expected frame with black border to be detected")
	}
}

func TestDetectFrameBordersIgnoresSingleDarkLineInsideContent(t *testing.T) {
	width := 80
	height := 48
	frame := make([]byte, width*height)
	fillFrameRect(frame, width, 0, 0, width, height, 150)

	for x := 0; x < width; x++ {
		frame[12*width+x] = 0
	}

	top, bottom, left, right, ok := detectFrameBorders(frame, width, height, defaultBlackBorderOptions)
	if !ok {
		t.Fatalf("expected frame analysis to succeed")
	}
	if top != 0 || bottom != 0 || left != 0 || right != 0 {
		t.Fatalf("expected no crop for single dark content line, got top=%d bottom=%d left=%d right=%d", top, bottom, left, right)
	}
}

func TestScaleCropRectSupportsAsymmetricCrop(t *testing.T) {
	rect := scaleCropRect(100, 200, 1080, 2400, 4, 0, 10, 20)
	if rect.X != 44 || rect.Y != 120 || rect.Width != 1036 || rect.Height != 2040 {
		t.Fatalf("unexpected crop rect %+v", rect)
	}
}

func TestScaleCropRectUsesInwardScalingToAvoidBlackLines(t *testing.T) {
	rect := scaleCropRect(100, 200, 1080, 2400, 4, 4, 10, 20)
	if rect.X != 44 || rect.Y != 120 || rect.Width != 992 || rect.Height != 2040 {
		t.Fatalf("unexpected crop rect %+v", rect)
	}
}

func TestScaleCropRectLegacyRequiresPairedMargins(t *testing.T) {
	rect := scaleCropRectLegacy(100, 200, 1080, 2400, 4, 0, 10, 10)
	if rect.X != 0 || rect.Width != 1080 {
		t.Fatalf("expected horizontal legacy crop to be skipped without paired margins, got %+v", rect)
	}
	if rect.Y != 120 || rect.Height != 2160 {
		t.Fatalf("expected vertical legacy crop to stay paired, got %+v", rect)
	}
}

func TestScaleCropRectLegacyUsesSymmetricPairCrop(t *testing.T) {
	rect := scaleCropRectLegacy(100, 200, 1080, 2400, 6, 4, 12, 10)
	if rect.X != 42 || rect.Width != 996 {
		t.Fatalf("expected symmetric legacy crop based on smaller pair, got %+v", rect)
	}
	if rect.Y != 120 || rect.Height != 2160 {
		t.Fatalf("expected symmetric vertical legacy crop based on smaller pair, got %+v", rect)
	}
}

func fillFrameRect(frame []byte, width int, x int, y int, rectWidth int, rectHeight int, value byte) {
	for row := y; row < y+rectHeight; row++ {
		for col := x; col < x+rectWidth; col++ {
			frame[row*width+col] = value
		}
	}
}
