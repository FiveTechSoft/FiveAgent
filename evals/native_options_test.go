package evals

import "testing"

func TestBatteryNativeOptions(t *testing.T) {
	for _, name := range []string{"FIVEAGENT_EVAL_NUM_CTX", "FIVEAGENT_EVAL_NUM_THREAD"} {
		for _, tc := range []struct {
			raw  string
			want int
			bad  bool
		}{{"", 0, false}, {"0", 0, false}, {"8192", 8192, false}, {"-1", 0, true}, {"oops", 0, true}} {
			t.Setenv(name, tc.raw)
			n, err := evalNativeInt(name)
			if (err != nil) != tc.bad || n != tc.want {
				t.Fatalf("%s=%q: %d %v", name, tc.raw, n, err)
			}
		}
	}
}
