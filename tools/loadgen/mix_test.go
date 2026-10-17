package main

import "testing"

// TestParseMix covers the ratio contract. The mixed-load mode is only useful if
// the pattern reproduces the requested proportions exactly, because that is what
// makes two runs of different lengths comparable.
func TestParseMix(t *testing.T) {
	cases := []struct {
		spec    string
		want    []uint8
		wantErr bool
	}{
		{spec: "", want: nil},
		{spec: "qos0:7,qos1:3", want: []uint8{0, 0, 0, 0, 0, 0, 0, 1, 1, 1}},
		{spec: "qos1:1", want: []uint8{1}},
		{spec: "qos0:2, qos2:1", want: []uint8{0, 0, 2}},
		{spec: "qos3:1", wantErr: true},
		{spec: "qos0:0", wantErr: true},
		{spec: "qos0", wantErr: true},
		{spec: "bad:1", wantErr: true},
		{spec: "qos0:1001", wantErr: true},
	}

	for _, c := range cases {
		got, err := parseMix(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseMix(%q) = %v, want error", c.spec, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMix(%q) unexpected error: %v", c.spec, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parseMix(%q) len = %d, want %d (%v)", c.spec, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseMix(%q)[%d] = %d, want %d", c.spec, i, got[i], c.want[i])
			}
		}
	}
}

// TestMaxQoS pins the subscription rule: subscribers must ask for the highest QoS
// in the mix, or the broker down-grades deliveries and the mixed run measures the
// wrong thing.
func TestMaxQoS(t *testing.T) {
	if got := maxQoS([]uint8{0, 0, 1}); got != 1 {
		t.Fatalf("maxQoS([0 0 1]) = %d, want 1", got)
	}
	if got := maxQoS([]uint8{2, 0}); got != 2 {
		t.Fatalf("maxQoS([2 0]) = %d, want 2", got)
	}
	if got := maxQoS(nil); got != 0 {
		t.Fatalf("maxQoS(nil) = %d, want 0", got)
	}
}
