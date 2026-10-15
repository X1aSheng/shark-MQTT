package broker

import "testing"

// TestFlappingWiring_OptionReachesBroker guards the wiring the detector unit
// tests cannot see: that an enabled detector installed through the option is
// the one the broker consults, and that a disabled one is dropped.
func TestFlappingWiring_OptionReachesBroker(t *testing.T) {
	t.Run("enabled is installed", func(t *testing.T) {
		d := NewFlappingDetector(FlappingConfig{Enabled: true, MaxConnects: 2})
		defer d.Stop()

		o := &brokerOptions{}
		WithFlappingDetection(d)(o)

		if o.flapping != d {
			t.Fatal("option did not store the detector")
		}
	})

	t.Run("disabled is dropped", func(t *testing.T) {
		d := NewFlappingDetector(FlappingConfig{Enabled: false})
		defer d.Stop()

		o := &brokerOptions{}
		WithFlappingDetection(d)(o)

		if o.flapping != nil {
			t.Error("a disabled detector was installed; it should be dropped")
		}
	})

	t.Run("nil is safe", func(t *testing.T) {
		o := &brokerOptions{}
		WithFlappingDetection(nil)(o)
		if o.flapping != nil {
			t.Error("nil detector installed")
		}
	})
}
