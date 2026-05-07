package defects

import (
	"testing"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestDefectTopicFiltersMayContainEmptyLevels(t *testing.T) {
	tests := []struct {
		filter string
		topic  string
	}{
		{filter: "/finance", topic: "/finance"},
		{filter: "finance/", topic: "finance/"},
		{filter: "finance//usd", topic: "finance//usd"},
		{filter: "finance/+", topic: "finance/"},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			if !protocol.ValidateTopicFilter(tt.filter) {
				t.Fatalf("ValidateTopicFilter(%q) = false, want true", tt.filter)
			}

			tree := broker.NewTopicTree()
			if !tree.Subscribe(tt.filter, "client", 1) {
				t.Fatalf("Subscribe(%q) rejected a spec-valid topic filter", tt.filter)
			}
			if got := tree.Match(tt.topic); len(got) != 1 || got[0].ClientID != "client" {
				t.Fatalf("Match(%q) = %#v, want one subscriber", tt.topic, got)
			}
		})
	}
}
