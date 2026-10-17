package main

import "testing"

// TestParsePayloadMix covers the schedule contract. The mixed payload mode exists
// to model a device fleet where heartbeats and telemetry share a broker; if the
// ratio is approximate, the two runs being compared are not the same workload.
func TestParsePayloadMix(t *testing.T) {
	cases := []struct {
		spec      string
		wantSizes []int
		wantLen   int
		wantErr   bool
	}{
		{spec: "", wantSizes: nil},
		{spec: "64:8,4096:2", wantSizes: []int{64, 4096}, wantLen: 10},
		{spec: "256:1", wantSizes: []int{256}, wantLen: 1},
		{spec: "64:2, 64:1", wantSizes: []int{64}, wantLen: 3}, // same size merges
		{spec: "4:1", wantErr: true},                           // below the timestamp
		{spec: "64:0", wantErr: true},
		{spec: "64", wantErr: true},
		{spec: "abc:1", wantErr: true},
		{spec: "64:1001", wantErr: true}, // expansion cap
	}

	for _, c := range cases {
		plan, err := parsePayloadMix(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePayloadMix(%q) = %+v, want error", c.spec, plan)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePayloadMix(%q) unexpected error: %v", c.spec, err)
			continue
		}
		if c.wantSizes == nil {
			if plan != nil {
				t.Errorf("parsePayloadMix(%q) = %+v, want nil", c.spec, plan)
			}
			continue
		}
		if len(plan.sizes) != len(c.wantSizes) {
			t.Errorf("parsePayloadMix(%q) sizes = %v, want %v", c.spec, plan.sizes, c.wantSizes)
			continue
		}
		for i := range plan.sizes {
			if plan.sizes[i] != c.wantSizes[i] {
				t.Errorf("parsePayloadMix(%q) sizes[%d] = %d, want %d", c.spec, i, plan.sizes[i], c.wantSizes[i])
			}
		}
		if len(plan.pattern) != c.wantLen {
			t.Errorf("parsePayloadMix(%q) pattern len = %d, want %d", c.spec, len(plan.pattern), c.wantLen)
		}
		for _, buf := range plan.bufs {
			if len(buf) < 8 {
				t.Errorf("parsePayloadMix(%q) produced a %d-byte buffer, need at least 8", c.spec, len(buf))
			}
		}
	}
}

// TestPayloadPlanFrameRatios pins the actual ratio observed over a window, which
// is the property the pattern approach was chosen for.
func TestPayloadPlanFrameRatios(t *testing.T) {
	plan, err := parsePayloadMix("64:8,4096:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	small, large := 0, 0
	for i := int64(0); i < 1000; i++ {
		switch len(plan.frame(i)) {
		case 64:
			small++
		case 4096:
			large++
		default:
			t.Fatalf("unexpected frame size %d", len(plan.frame(i)))
		}
	}
	if small != 800 || large != 200 {
		t.Fatalf("over 1000 frames got %d small / %d large, want 800 / 200", small, large)
	}
}
