package tokenizer

import "testing"

func TestEstimate(t *testing.T) {
	tests := []struct {
		n    int64
		want int64
	}{
		{0, 0},
		{-5, 0},
		{1, 1},
		{4, 1},
		{5, 2},
		{4096, 1024},
	}
	for _, tt := range tests {
		if got := Estimate(tt.n); got != tt.want {
			t.Errorf("Estimate(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}
