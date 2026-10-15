package integration

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// mgmtBroker starts a broker with the management endpoints enabled. When token
// is non-empty, every management request must present it.
func mgmtBroker(t *testing.T, token string) (*api.Broker, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.MetricsAddr = "127.0.0.1:0"
	cfg.ManagementEnabled = true
	cfg.ManagementToken = token

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })
	return brk, "http://" + brk.MetricsAddr()
}

// mgmtGet issues a GET, bypassing the environment's proxy: the metrics address
// is loopback and a proxy would answer for it.
func mgmtGet(t *testing.T, base, path, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // loopback must not go through a proxy
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// subscribeClient connects a client and subscribes it to one filter.
func subscribeClient(t *testing.T, b *api.Broker, clientID, filter string, qos uint8) net.Conn {
	t.Helper()
	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	connectSubscriber(t, conn, codec, clientID)

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: filter, QoS: qos}},
	}); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	conn.SetDeadline(time.Time{})
	return conn
}

// TestManagement_DisabledByDefault is the security guard: without the opt-in,
// the endpoints must not exist at all. An unauthenticated /clients would turn
// the broker into a directory of who is connected to what.
func TestManagement_DisabledByDefault(t *testing.T) {
	brk := testBroker(t) // default config: management_enabled is false
	base := "http://" + brk.MetricsAddr()

	for _, path := range []string{"/clients", "/topics", "/clients/anyone"} {
		code, _ := mgmtGet(t, base, path, "")
		if code != http.StatusNotFound {
			t.Errorf("GET %s = %d with management disabled, want 404", path, code)
		}
	}
}

// TestManagement_ClientsListsSessions covers the happy path and the payload
// shape an operator actually depends on.
func TestManagement_ClientsListsSessions(t *testing.T) {
	brk, base := mgmtBroker(t, "")
	subscribeClient(t, brk, "mgmt-alice", "sensors/room1/+/temp", 1)
	subscribeClient(t, brk, "mgmt-bob", "alerts/#", 0)
	waitForConnCount(t, brk, 2)

	code, body := mgmtGet(t, base, "/clients", "")
	if code != http.StatusOK {
		t.Fatalf("GET /clients = %d, want 200: %s", code, body)
	}

	var payload struct {
		Count   int               `json:"count"`
		Online  int64             `json:"online"`
		Clients []json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if payload.Count != 2 {
		t.Errorf("count = %d, want 2", payload.Count)
	}
	if payload.Online != 2 {
		t.Errorf("online = %d, want 2", payload.Online)
	}

	// Every client must report online and carry its subscriptions.
	for _, raw := range payload.Clients {
		var c struct {
			ClientID      string           `json:"client_id"`
			Online        bool             `json:"online"`
			Subscriptions map[string]uint8 `json:"subscriptions"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("decode client: %v", err)
		}
		if !c.Online {
			t.Errorf("client %q reported offline while connected", c.ClientID)
		}
		if len(c.Subscriptions) == 0 {
			t.Errorf("client %q has no subscriptions reported", c.ClientID)
		}
	}
}

// TestManagement_ClientByID covers the path-parameter route, including the 404.
func TestManagement_ClientByID(t *testing.T) {
	brk, base := mgmtBroker(t, "")
	subscribeClient(t, brk, "mgmt-carol", "devices/a", 1)
	waitForConnCount(t, brk, 1)

	code, body := mgmtGet(t, base, "/clients/mgmt-carol", "")
	if code != http.StatusOK {
		t.Fatalf("GET /clients/mgmt-carol = %d: %s", code, body)
	}
	var got struct {
		ClientID      string           `json:"client_id"`
		Subscriptions map[string]uint8 `json:"subscriptions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got.ClientID != "mgmt-carol" {
		t.Errorf("client_id = %q, want mgmt-carol", got.ClientID)
	}
	if qos, ok := got.Subscriptions["devices/a"]; !ok || qos != 1 {
		t.Errorf("subscriptions = %v, want devices/a at QoS 1", got.Subscriptions)
	}

	if code, _ := mgmtGet(t, base, "/clients/nobody", ""); code != http.StatusNotFound {
		t.Errorf("GET /clients/nobody = %d, want 404", code)
	}
}

// TestManagement_Topics covers the topic listing, the server-side filter, and
// that shared subscriptions are reported distinctly from ordinary ones.
func TestManagement_Topics(t *testing.T) {
	brk, base := mgmtBroker(t, "")
	subscribeClient(t, brk, "mgmt-sub1", "sensors/+/temp", 1)
	subscribeClient(t, brk, "mgmt-sub2", "alerts/#", 0)
	subscribeClient(t, brk, "mgmt-share", "$share/grp/sensors/room1", 1)
	waitForConnCount(t, brk, 3)

	code, body := mgmtGet(t, base, "/topics", "")
	if code != http.StatusOK {
		t.Fatalf("GET /topics = %d: %s", code, body)
	}
	var payload struct {
		Count  int `json:"count"`
		Topics []struct {
			Filter      string           `json:"filter"`
			Shared      bool             `json:"shared"`
			Group       string           `json:"group"`
			Subscribers map[string]uint8 `json:"subscribers"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}

	byFilter := make(map[string]int)
	for i, st := range payload.Topics {
		byFilter[st.Filter+"|"+st.Group] = i
	}
	for _, want := range []string{"sensors/+/temp|", "alerts/#|", "sensors/room1|grp"} {
		if _, ok := byFilter[want]; !ok {
			t.Errorf("topic %q missing from listing: %s", want, body)
		}
	}
	if st := payload.Topics[byFilter["sensors/room1|grp"]]; !st.Shared {
		t.Error("shared subscription not flagged as shared")
	}
	if st := payload.Topics[byFilter["alerts/#|"]]; st.Shared {
		t.Error("ordinary subscription wrongly flagged as shared")
	}

	// Server-side filter narrows the result.
	code, body = mgmtGet(t, base, "/topics?filter=alerts/%23", "")
	if code != http.StatusOK {
		t.Fatalf("GET /topics?filter= = %d: %s", code, body)
	}
	var filtered struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if filtered.Count != 1 {
		t.Errorf("filtered count = %d, want 1\n%s", filtered.Count, body)
	}
}

// TestManagement_TokenRequired covers the auth gate: wrong or missing tokens
// must be rejected, and the correct one accepted via both header and query.
func TestManagement_TokenRequired(t *testing.T) {
	const token = "s3cret-token"
	brk, base := mgmtBroker(t, token)
	subscribeClient(t, brk, "mgmt-auth", "auth/topic", 1)

	for _, path := range []string{"/clients", "/topics", "/clients/mgmt-auth"} {
		if code, _ := mgmtGet(t, base, path, ""); code != http.StatusUnauthorized {
			t.Errorf("GET %s without token = %d, want 401", path, code)
		}
		if code, _ := mgmtGet(t, base, path, "wrong"); code != http.StatusUnauthorized {
			t.Errorf("GET %s with wrong token = %d, want 401", path, code)
		}
		if code, _ := mgmtGet(t, base, path, token); code != http.StatusOK {
			t.Errorf("GET %s with correct token = %d, want 200", path, code)
		}
	}

	// The query-string form must work too, for `curl` convenience.
	code, body := mgmtGet(t, base, "/clients?token="+token, "")
	if code != http.StatusOK {
		t.Errorf("GET /clients?token= = %d: %s", code, body)
	}
}

// TestManagement_RejectsNonGET proves the endpoints are read-only in practice,
// not merely by documentation.
func TestManagement_RejectsNonGET(t *testing.T) {
	_, base := mgmtBroker(t, "")
	req, err := http.NewRequest(http.MethodDelete, base+"/clients/mgmt-x", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /clients/x = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow header = %q, want it to mention GET", allow)
	}
}

// TestManagement_WarnsWhenExposedWithoutToken covers the config guard: enabling
// management on a non-loopback metrics address with no token must fail
// validation, because that is the "exposed to the world" case.
func TestManagement_WarnsWhenExposedWithoutToken(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = "0.0.0.0:19000"
	cfg.ManagementEnabled = true
	cfg.ManagementToken = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for unauthenticated non-loopback management")
	}
	if !strings.Contains(err.Error(), "management_token") {
		t.Errorf("error does not mention management_token: %v", err)
	}

	// Loopback without a token is fine, and so is a token on any address.
	cfg.MetricsAddr = "127.0.0.1:19000"
	if err := cfg.Validate(); err != nil {
		t.Errorf("loopback without token rejected: %v", err)
	}
	cfg.MetricsAddr = "0.0.0.0:19000"
	cfg.ManagementToken = "t"
	if err := cfg.Validate(); err != nil {
		t.Errorf("non-loopback with token rejected: %v", err)
	}
}

// TestManagement_ClientIDNeedingEscaping proves the /clients/{id} route decodes
// percent-escapes instead of looking up the escaped form.
func TestManagement_ClientIDNeedingEscaping(t *testing.T) {
	const clientID = "tenant a/b"
	brk, base := mgmtBroker(t, "")
	subscribeClient(t, brk, clientID, "esc/topic", 1)
	waitForConnCount(t, brk, 1)

	// PathEscape leaves '/' as-is only in path segments; encode explicitly.
	escaped := "tenant%20a%2Fb"
	code, body := mgmtGet(t, base, "/clients/"+escaped, "")
	if code != http.StatusOK {
		t.Fatalf("GET escaped client = %d: %s", code, body)
	}
	var got struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got.ClientID != clientID {
		t.Errorf("client_id = %q, want %q", got.ClientID, clientID)
	}
}
