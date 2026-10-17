// Command loadgen drives a shark-mqtt broker and reports throughput, latency
// distribution and the resources that throughput costs.
//
// It uses the project's own client package rather than a hand-rolled socket
// writer, so the numbers reflect a real client's code path (codec, inflight
// tracking, write queue) rather than an idealised one.
//
// Latency is measured end to end: the publisher stamps its send time into the
// payload, and each subscriber computes the difference when the message
// arrives. Timing the publish call alone would measure the local socket buffer
// and miss the broker entirely.
//
// Usage:
//
//	loadgen -addr 127.0.0.1:2883 -subscribers 4 -publishers 8 \
//	        -qos 1 -rate 20000 -payload 256 -duration 20
//
// The client runs on the same host as the broker unless you move it, so both
// compete for CPU. Results say so; treat them as a floor, not a ceiling.
package main

import (
	"net"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/client"
)

type config struct {
	addr        string
	user        string
	pass        string
	subscribers int
	publishers  int
	qos         int
	rate        int
	payload     int
	duration    time.Duration
	topic       string
	pid         int
	jsonOut     bool
	latency     bool

	// mode selects what the tool measures: "load" runs the publish/subscribe
	// benchmark, "idle" only establishes connections and holds them.
	mode string
	// connections is the connection count for idle mode; concurrency bounds how
	// many dials are in flight at once.
	connections  int
	idleParallel int
	// note is free text copied into the JSON _meta block. Numbers without the
	// environment they came from are not comparable three months later, and a
	// hand-written sidecar file is the first thing to be forgotten.
	note string
	// mix is a QoS ratio such as "qos0:70,qos1:30"; when set it overrides -qos
	// per message. sampleInterval, when positive, records a timeline point at
	// that cadence so a soak can show whether RSS climbs.
	mix            string
	sampleInterval time.Duration
	// payloadMix is a size ratio such as "64:80,4096:20"; when set it overrides
	// -payload per message.
	payloadMix string
	// online opens N connections that connect and stay idle, without
	// subscribing. A device fleet is not ten thousand subscribers of one topic;
	// it is ten thousand connections of which a few carry traffic.
	online int
}

var cfg config

func init() {
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:2883", "broker address")
	flag.StringVar(&cfg.user, "user", "", "username")
	flag.StringVar(&cfg.pass, "pass", "", "password")
	flag.IntVar(&cfg.subscribers, "subscribers", 4, "number of subscriber connections")
	flag.IntVar(&cfg.publishers, "publishers", 8, "number of publisher connections")
	flag.IntVar(&cfg.qos, "qos", 1, "QoS level 0, 1 or 2")
	flag.IntVar(&cfg.rate, "rate", 20000, "total messages per second across all publishers")
	flag.IntVar(&cfg.payload, "payload", 256, "payload bytes")
	flag.DurationVar(&cfg.duration, "duration", 20*time.Second, "how long to sustain load")
	flag.StringVar(&cfg.topic, "topic", "loadgen/topic", "topic to publish and subscribe")
	flag.IntVar(&cfg.pid, "pid", 0, "broker PID for resource sampling; 0 finds it automatically")
	flag.BoolVar(&cfg.jsonOut, "json", false, "emit JSON instead of a table")
	flag.BoolVar(&cfg.latency, "latency", true, "record end-to-end latency samples; "+
		"disable at very high subscriber counts, where the recording itself becomes load")
	flag.StringVar(&cfg.mode, "mode", "load", "what to measure: load (pub/sub benchmark) or idle (connections only)")
	flag.IntVar(&cfg.connections, "connections", 1000, "connection count for -mode idle")
	flag.IntVar(&cfg.idleParallel, "idle-parallel", 64, "concurrent dials for -mode idle")
	flag.StringVar(&cfg.note, "note", "", "free text stored in the JSON _meta block (host, topology, anything needed to interpret the run)")
	flag.StringVar(&cfg.mix, "mix", "", "QoS ratio, e.g. \"qos0:70,qos1:30\"; overrides -qos per message")
	flag.DurationVar(&cfg.sampleInterval, "sample-interval", 0, "record a timeline point every N (0 disables); use during soaks to see RSS trend")
	flag.StringVar(&cfg.payloadMix, "payload-mix", "", "payload size ratio, e.g. \"64:80,4096:20\"; overrides -payload per message")
	flag.IntVar(&cfg.online, "online", 0, "additional connections that connect but never subscribe (models an online fleet with a small active subset)")
}

// stats aggregates counters across all connections.
type stats struct {
	sent      atomic.Int64
	received  atomic.Int64
	errors    atomic.Int64
	connected atomic.Int64
}

// rssSample is one reading of the broker's resource use.
type rssSample struct {
	rss     int64 // bytes, current
	peakRSS int64 // bytes, kernel high-water mark
	threads int
	cpuNano int64 // cumulative CPU time
	at      time.Time
}

func main() {
	flag.Parse()

	if cfg.mode == "idle" {
		if cfg.connections <= 0 {
			fmt.Fprintln(os.Stderr, "need a positive -connections")
			os.Exit(2)
		}
		runIdle(cfg.addr, cfg.connections, cfg.duration, cfg.idleParallel)
		return
	}
	if cfg.mode != "load" {
		fmt.Fprintf(os.Stderr, "unknown -mode %q (want load or idle)\n", cfg.mode)
		os.Exit(2)
	}

	if cfg.publishers <= 0 || cfg.subscribers <= 0 {
		fmt.Fprintln(os.Stderr, "need at least one publisher and one subscriber")
		os.Exit(2)
	}

	mixPattern, err := parseMix(cfg.mix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	subQoS := byte(cfg.qos)
	if mixPattern != nil {
		subQoS = maxQoS(mixPattern)
	}

	payPlan, err := parsePayloadMix(cfg.payloadMix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	brokerPID := cfg.pid
	if brokerPID == 0 {
		brokerPID = findBrokerPID()
		if brokerPID == 0 {
			fmt.Fprintln(os.Stderr, "warning: broker process not found; "+
				"resource columns will be empty (pass -pid)")
		}
	}

	var st stats
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connections that stay online and silent. Without this, subscribers is
	// both "how many devices are connected" and "how many receive every
	// message", which conflates two very different costs: a fleet of ten
	// thousand devices does not fan out every publish to all of them.
	var onlineConns []net.Conn
	if cfg.online > 0 {
		onlineConns = make([]net.Conn, 0, cfg.online)
		var onlineMu sync.Mutex
		var onlineWG sync.WaitGroup
		onlineSem := make(chan struct{}, 256)
		for i := 0; i < cfg.online; i++ {
			onlineWG.Add(1)
			onlineSem <- struct{}{}
			go func(i int) {
				defer onlineWG.Done()
				defer func() { <-onlineSem }()
				// Offset the id space so these client IDs never collide with the
				// loadgen subscribers and publishers.
				c, err := dialAndConnect(cfg.addr, 1000000+i)
				if err != nil {
					st.errors.Add(1)
					return
				}
				onlineMu.Lock()
				onlineConns = append(onlineConns, c)
				onlineMu.Unlock()
			}(i)
		}
		onlineWG.Wait()
		fmt.Fprintf(os.Stderr, "online: %d silent connections established\n", len(onlineConns))
		defer func() {
			for _, c := range onlineConns {
				_ = c.Close()
			}
		}()
	}

	// Latency samples live per-subscriber so the hot path needs no lock.
	latency := make([][]int64, cfg.subscribers)
	subs := make([]*client.MQTTClient, cfg.subscribers)

	// Subscribers first: publishing before anyone listens measures nothing.
	//
	// They are established in parallel. At the 2000-connection scale this file
	// exists to reach, connecting one at a time costs several seconds of TCP
	// handshake and CONNECT before any load starts, and that setup time is not
	// what is being measured.
	var subWG sync.WaitGroup
	subErr := make(chan error, cfg.subscribers)
	sem := make(chan struct{}, 256)
	for i := 0; i < cfg.subscribers; i++ {
		subWG.Add(1)
		go func(i int) {
			defer subWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			c, err := dial(fmt.Sprintf("loadgen-sub-%d", i))
			if err != nil {
				subErr <- fmt.Errorf("subscriber %d: %w", i, err)
				return
			}
			subs[i] = c

			idx := i
			c.SetOnMessage(func(_ string, _ byte, payload []byte) {
				st.received.Add(1)
				if !cfg.latency || len(payload) < 8 {
					return
				}
				sent := int64(binary.BigEndian.Uint64(payload[:8]))
				latency[idx] = append(latency[idx], time.Now().UnixNano()-sent)
			})
			if _, err := c.Subscribe(ctx, []client.TopicSubscription{
				{Topic: cfg.topic, QoS: subQoS},
			}); err != nil {
				subErr <- fmt.Errorf("subscriber %d subscribe: %w", i, err)
				return
			}
			st.connected.Add(1)
		}(i)
	}
	subWG.Wait()
	close(subErr)
	if err := <-subErr; err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Latency buffers are sized after connections exist, because a subscriber
	// count in the thousands makes the per-connection estimate matter.
	perSub := cfg.rate * int(cfg.duration.Seconds()) / cfg.subscribers / 4
	if perSub < 512 {
		perSub = 512
	}
	for i := range latency {
		latency[i] = make([]int64, 0, perSub)
	}

	// Resource sampling starts before load so the ramp is captured.
	samples := make([]rssSample, 0, 256)
	if brokerPID > 0 {
		samples = append(samples, readResources(brokerPID))
	}
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if brokerPID > 0 {
					samples = append(samples, readResources(brokerPID))
				}
			}
		}
	}()

	payload := makePayload(cfg.payload)
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < cfg.publishers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, err := dial(fmt.Sprintf("loadgen-pub-%d", idx))
			if err != nil {
				st.errors.Add(1)
				return
			}
			defer func() {
				// Disconnect is best-effort: the broker is about to lose the
				// client anyway when the process exits, and a failure here
				// would mask a real publish error in the report.
				_ = c.Disconnect(context.Background())
			}()
			st.connected.Add(1)

			// Each publisher paces itself so the aggregate hits the target
			// rate. Sleeping the remainder rather than a fixed interval keeps
			// the achieved rate close to the requested one even when a publish
			// occasionally takes longer than the interval.
			perPublisher := float64(cfg.rate) / float64(cfg.publishers)
			interval := time.Duration(0)
			if perPublisher > 0 {
				interval = time.Duration(float64(time.Second) / perPublisher)
			}
			next := time.Now()
			for {
				if ctx.Err() != nil {
					return
				}
				frame := payload
				if payPlan != nil {
					frame = payPlan.frame(st.sent.Load())
				}
				binary.BigEndian.PutUint64(frame[:8], uint64(time.Now().UnixNano()))
				pubQoS := byte(cfg.qos)
				if mixPattern != nil {
					// Shared across publishers: the global send counter keeps the
					// ratio exact without per-publisher bookkeeping.
					pubQoS = mixPattern[int(st.sent.Load())%len(mixPattern)]
				}
				if err := c.Publish(ctx, cfg.topic, pubQoS, false, frame); err != nil {
					if ctx.Err() != nil {
						return
					}
					st.errors.Add(1)
					continue
				}
				st.sent.Add(1)
				if interval > 0 {
					next = next.Add(interval)
					if d := time.Until(next); d > 0 {
						time.Sleep(d)
					}
				}
			}
		}(i)
	}

	time.Sleep(cfg.duration)
	cancel()
	wg.Wait()
	<-samplerDone

	if brokerPID > 0 {
		samples = append(samples, readResources(brokerPID))
	}

	var all []int64
	for _, l := range latency {
		all = append(all, l...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	elapsed := time.Since(start).Seconds()
	report(&st, all, samples, elapsed, brokerPID)

	for _, c := range subs {
		_ = c.Disconnect(context.Background())
	}
}

func makePayload(n int) []byte {
	if n < 8 {
		n = 8
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func dial(clientID string) (*client.MQTTClient, error) {
	opts := []client.Option{
		client.WithAddr(cfg.addr),
		client.WithClientID(clientID),
		client.WithCleanSession(true),
		client.WithKeepAlive(60),
	}
	if cfg.user != "" {
		opts = append(opts, client.WithCredentials(cfg.user, cfg.pass))
	}
	c := client.New(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// findBrokerPID locates the broker by scanning /proc. Used when the caller does
// not pass -pid, so a run does not silently report empty resource numbers.
func findBrokerPID() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := string(raw)
		if strings.Contains(cmd, "shark") && !strings.Contains(cmd, "loadgen") {
			return pid
		}
	}
	return 0
}

// readResources reads RSS, the kernel's peak-RSS watermark and cumulative CPU
// time. Peak RSS comes from VmHWM because the current value underestimates what
// a connection storm actually cost: the high-water mark survives GC.
func readResources(pid int) rssSample {
	s := rssSample{at: time.Now()}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "VmRSS:":
				s.rss, _ = strconv.ParseInt(fields[1], 10, 64)
				s.rss *= 1024
			case "VmHWM:":
				s.peakRSS, _ = strconv.ParseInt(fields[1], 10, 64)
				s.peakRSS *= 1024
			case "Threads:":
				s.threads, _ = strconv.Atoi(fields[1])
			}
		}
	}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 15 {
			utime, _ := strconv.ParseInt(fields[13], 10, 64)
			stime, _ := strconv.ParseInt(fields[14], 10, 64)
			// 100 ticks/sec is the Linux default; the ratio is what matters.
			s.cpuNano = (utime + stime) * 10 * int64(time.Millisecond)
		}
	}
	return s
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// buildRevision returns the VCS revision stamped into the binary by the Go
// toolchain, so a measurement always points back at the code that produced it.
func buildRevision() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				if s.Value == "" {
					return "unknown"
				}
				return s.Value
			}
		}
	}
	return "unknown"
}

// timelineFrom downsamples the resource samples to one point per interval.
//
// A soak produces a sample every 250 ms; half an hour of that is 7,200 points in
// one JSON file, which hides the trend in noise instead of showing it. One point
// per interval keeps the file readable and still exposes a monotonic climb,
// which is the only thing a soak is looking for.
func timelineFrom(samples []rssSample, every time.Duration) []map[string]any {
	if every <= 0 || len(samples) == 0 {
		return nil
	}
	start := samples[0].at
	out := make([]map[string]any, 0, 32)
	next := start
	for _, s := range samples {
		if s.at.Before(next) {
			continue
		}
		next = s.at.Add(every)
		out = append(out, map[string]any{
			"t_s":      s.at.Sub(start).Seconds(),
			"rss_mb":   float64(s.rss) / (1 << 20),
			"peak_mb":  float64(s.peakRSS) / (1 << 20),
			"threads":  s.threads,
			"cpu_nano": s.cpuNano,
		})
	}
	return out
}

func report(st *stats, lat []int64, samples []rssSample, elapsed float64, pid int) {
	sent := st.sent.Load()
	recv := st.received.Load()
	errs := st.errors.Load()

	out := map[string]any{
		"config": map[string]any{
			"addr": cfg.addr, "subscribers": cfg.subscribers, "publishers": cfg.publishers,
			"qos": cfg.qos, "rate_target": cfg.rate, "payload": cfg.payload,
			"mix": cfg.mix, "payload_mix": cfg.payloadMix, "online": cfg.online,
			"duration_s": cfg.duration.Seconds(),
		},
		"sent": sent, "received": recv, "errors": errs,
		"elapsed_s":           elapsed,
		"send_rate_msg_s":     float64(sent) / elapsed,
		"deliver_rate_msg_s":  float64(recv) / elapsed,
		"delivery_ratio":      ratio(recv, sent),
		"latency_samples":     len(lat),
		"latency_p50_us":      percentile(lat, 50) / 1000,
		"latency_p90_us":      percentile(lat, 90) / 1000,
		"latency_p99_us":      percentile(lat, 99) / 1000,
		"latency_p999_us":     percentile(lat, 99.9) / 1000,
		"latency_max_us":      percentile(lat, 100) / 1000,
		"latency_mean_us":     mean(lat) / 1000,
		"broker_pid":          pid,
		"broker_rss_avg_mb":   avgRSS(samples),
		"broker_rss_peak_mb":  peakRSS(samples),
		"broker_threads_peak": peakThreads(samples),
		"broker_cpu_cores":    cpuCores(samples),
	}

	// _meta travels with the numbers. A bare throughput figure is unreadable
	// three months later: it needs the commit it was built from, the platform,
	// and whatever the operator knows about the topology.
	out["_meta"] = map[string]any{
		"tool":       "loadgen",
		"commit":     buildRevision(),
		"timestamp":  time.Now().Format(time.RFC3339),
		"go_version": runtime.Version(),
		"platform":   runtime.GOOS + "/" + runtime.GOARCH,
		"mode":       cfg.mode,
		"note":       cfg.note,
		"resource_sampling": "broker_* fields need -pid and a readable /proc; " +
			"they are zero on Windows",
	}

	if tl := timelineFrom(samples, cfg.sampleInterval); len(tl) > 0 {
		out["rss_timeline"] = tl
	}

	if cfg.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	qosDesc := fmt.Sprintf("QoS%d", cfg.qos)
	if cfg.mix != "" {
		qosDesc = "QoS mix " + cfg.mix
	}
	payDesc := fmt.Sprintf("%dB", cfg.payload)
	if cfg.payloadMix != "" {
		payDesc = "payload mix " + cfg.payloadMix
	}
	fmt.Printf("配置       : %d 订阅者 / %d 发布者, %s, %s, 目标 %d msg/s\n",
		cfg.subscribers, cfg.publishers, qosDesc, payDesc, cfg.rate)
	fmt.Printf("时长       : %.1fs（实际 %.1fs）\n", cfg.duration.Seconds(), elapsed)
	fmt.Println()
	fmt.Printf("发送       : %d 条  (%.0f msg/s)\n", sent, float64(sent)/elapsed)
	fmt.Printf("投递       : %d 条  (%.0f msg/s)\n", recv, float64(recv)/elapsed)
	fmt.Printf("投递率     : %.2f%%   错误: %d\n", ratio(recv, sent)*100, errs)
	fmt.Println()
	fmt.Printf("延迟(端到端, %d 样本)\n", len(lat))
	fmt.Printf("  P50  %8.1f us\n", float64(percentile(lat, 50))/1000)
	fmt.Printf("  P90  %8.1f us\n", float64(percentile(lat, 90))/1000)
	fmt.Printf("  P99  %8.1f us\n", float64(percentile(lat, 99))/1000)
	fmt.Printf("  P999 %8.1f us\n", float64(percentile(lat, 99.9))/1000)
	fmt.Printf("  max  %8.1f us\n", float64(percentile(lat, 100))/1000)
	fmt.Println()
	if pid > 0 {
		fmt.Printf("broker 资源 (PID %d)\n", pid)
		fmt.Printf("  RSS 均值   %8.1f MB\n", avgRSS(samples))
		fmt.Printf("  RSS 峰值   %8.1f MB  (VmHWM)\n", peakRSS(samples))
		fmt.Printf("  线程峰值   %8d\n", peakThreads(samples))
		fmt.Printf("  CPU        %8.2f 核\n", cpuCores(samples))
	} else {
		fmt.Println("broker 资源: 未采集（未找到进程）")
	}
}

func ratio(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func mean(v []int64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum int64
	for _, x := range v {
		sum += x
	}
	return float64(sum) / float64(len(v))
}

func avgRSS(s []rssSample) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum int64
	for _, x := range s {
		sum += x.rss
	}
	return float64(sum) / float64(len(s)) / 1024 / 1024
}

func peakRSS(s []rssSample) float64 {
	var m int64
	for _, x := range s {
		if x.peakRSS > m {
			m = x.peakRSS
		}
	}
	return float64(m) / 1024 / 1024
}

func peakThreads(s []rssSample) int {
	m := 0
	for _, x := range s {
		if x.threads > m {
			m = x.threads
		}
	}
	return m
}

// cpuCores averages the CPU used over the sampling window and expresses it in
// cores, which is the unit an operator sizes a container with. A single
// instantaneous sample would report a spike.
func cpuCores(s []rssSample) float64 {
	if len(s) < 2 {
		return 0
	}
	first, last := s[0], s[len(s)-1]
	wall := last.at.Sub(first.at).Seconds()
	if wall <= 0 {
		return 0
	}
	return float64(last.cpuNano-first.cpuNano) / 1e9 / wall
}
