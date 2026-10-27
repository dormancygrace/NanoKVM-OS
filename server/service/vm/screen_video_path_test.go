package vm

import "testing"

func TestCurrentVideoPath(t *testing.T) {
	for _, tc := range []struct {
		name            string
		active          bool
		fps, path, want int
	}{
		{"stopped video with continuing MJPEG", false, 30, 1, 0},
		{"stopped video with retained VPSS", false, 30, 3, 0},
		{"active session without frames", true, 0, 1, 0},
		{"missing path on an older library", true, 60, 0, 0},
		{"VI direct", true, 60, 1, 1},
		{"VPSS setting", true, 60, 2, 2},
		{"VPSS rate", true, 30, 3, 3},
		{"VPSS size", true, 60, 4, 4},
		{"VPSS shared reader", true, 60, 5, 5},
		{"VPSS unsupported", true, 60, 6, 6},
		{"VPSS other", true, 60, 7, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) int {
				switch path {
				case "/run/nanokvm/now_fps":
					return tc.fps
				case "/run/nanokvm/video_path":
					return tc.path
				default:
					t.Fatalf("unexpected status read %s", path)
					return 0
				}
			}
			if got := currentVideoPath(tc.active, read); got != tc.want {
				t.Fatalf("video path = %d, want %d", got, tc.want)
			}
		})
	}
}
