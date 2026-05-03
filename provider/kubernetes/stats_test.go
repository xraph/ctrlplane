package kubernetes

import "testing"

// TestParseCPUQuantity covers every shape metrics-server emits in
// practice: nanocpus ("12345678n"), millicpus ("250m"), and bare
// cores ("0.5") via the upstream resource.ParseQuantity path.
func TestParseCPUQuantity(t *testing.T) {
	cases := []struct {
		in   string
		want int64 // millicpus
	}{
		{"", 0},
		{"250m", 250},
		{"0.5", 500},
		{"1", 1000},
		{"100000000n", 100}, // 100ms = 100m
		{"500000000n", 500}, // 500m
		{"garbage", 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := parseCPUQuantity(c.in); got != c.want {
				t.Fatalf("parseCPUQuantity(%q): want %d, got %d", c.in, c.want, got)
			}
		})
	}
}

// TestParseMemoryQuantity covers Ki/Mi/Gi suffixes plus raw bytes.
func TestParseMemoryQuantity(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"1024", 1024},
		{"1Ki", 1024},
		{"1Mi", 1024 * 1024},
		{"512Mi", 512 * 1024 * 1024},
		{"1Gi", 1024 * 1024 * 1024},
		{"garbage", 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := parseMemoryQuantity(c.in); got != c.want {
				t.Fatalf("parseMemoryQuantity(%q): want %d, got %d", c.in, c.want, got)
			}
		})
	}
}
