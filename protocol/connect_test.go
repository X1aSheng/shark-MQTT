package protocol

import "testing"

func TestValidateConnectProtocolNameMatchesVersion(t *testing.T) {
	tests := []struct {
		name       string
		protoName  string
		protoLevel byte
		wantErr    bool
	}{
		{
			name:       "mqtt 3.1.1",
			protoName:  ProtocolNameMQTT,
			protoLevel: Version311,
		},
		{
			name:       "mqtt 5.0",
			protoName:  ProtocolNameMQTT,
			protoLevel: Version50,
		},
		{
			name:       "mqtt 3.1 legacy",
			protoName:  ProtocolNameMQIsdp,
			protoLevel: Version31,
		},
		{
			name:       "mqisdp with mqtt 3.1.1 level",
			protoName:  ProtocolNameMQIsdp,
			protoLevel: Version311,
			wantErr:    true,
		},
		{
			name:       "mqtt with mqtt 3.1 legacy level",
			protoName:  ProtocolNameMQTT,
			protoLevel: Version31,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnect(&ConnectPacket{
				ProtocolName:    tt.protoName,
				ProtocolVersion: tt.protoLevel,
				Flags: ConnectFlags{
					CleanSession: true,
				},
				ClientID: "client",
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConnect() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
