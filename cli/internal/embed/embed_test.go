package embed

import (
	"math"
	"testing"
)

func TestMeanPool(t *testing.T) {
	tests := []struct {
		name string
		in   [][]float32
		want []float32
	}{
		{"single vector is itself", [][]float32{{1, 2, 3}}, []float32{1, 2, 3}},
		{"element-wise mean", [][]float32{{1, 1}, {3, 5}}, []float32{2, 3}},
		{"three vectors", [][]float32{{0, 0}, {3, 6}, {6, 0}}, []float32{3, 2}},
		{"negatives average", [][]float32{{-2, 4}, {2, -4}}, []float32{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := meanPool(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if math.Abs(float64(got[i]-tt.want[i])) > 1e-6 {
					t.Errorf("[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
