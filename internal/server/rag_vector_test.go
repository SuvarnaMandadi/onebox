package server

import (
	"math"
	"testing"
)

func TestEncodeDecodeVectorRoundTrip(t *testing.T) {
	original := []float32{0.1, -0.5, 3.14159, 0, -1000.5}
	decoded := decodeVector(encodeVector(original))

	if len(decoded) != len(original) {
		t.Fatalf("got %d values, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("index %d: got %v, want %v", i, decoded[i], original[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{name: "identical vectors", a: []float32{1, 2, 3}, b: []float32{1, 2, 3}, want: 1},
		{name: "opposite vectors", a: []float32{1, 0}, b: []float32{-1, 0}, want: -1},
		{name: "orthogonal vectors", a: []float32{1, 0}, b: []float32{0, 1}, want: 0},
		{name: "mismatched lengths", a: []float32{1, 2}, b: []float32{1}, want: 0},
		{name: "zero vector", a: []float32{0, 0}, b: []float32{1, 1}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cosineSimilarity(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineSimilarityRanksMoreSimilarHigher(t *testing.T) {
	query := []float32{1, 1, 0}
	closeMatch := []float32{1, 0.9, 0}
	farMatch := []float32{0, 0, 1}

	closeScore := cosineSimilarity(query, closeMatch)
	farScore := cosineSimilarity(query, farMatch)

	if closeScore <= farScore {
		t.Fatalf("expected closer vector to score higher: close=%v far=%v", closeScore, farScore)
	}
}
