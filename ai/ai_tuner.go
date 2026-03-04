package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gostream/internal/gostorm/settings"
	"gostream/internal/gostorm/torr"
	"gostream/internal/gostorm/torr/state"
)

var lastConns = 30
var lastTimeout = 30
var metricsHistory []string
var lastKnownTotalSpeed float64
var CurrentLimit int32

// V1.6.17: Rolling averages (120s window, 24 samples every 5s)
var torrentSpeedAvg []float64
var totalSpeedAvg []float64
var cpuUsageAvg []float64
var cycleCounter int
var pulseCounter int

type AITweak struct {
	ConnectionsLimit float64 `json:"connections_limit"`
	PeerTimeout      float64 `json:"peer_timeout"`
}

func (t *AITweak) Sanitize() {
	if t.ConnectionsLimit < 15 {
		t.ConnectionsLimit = 15
	}
	if t.ConnectionsLimit > 60 {
		t.ConnectionsLimit = 60
	}
	if t.PeerTimeout < 15 {
		t.PeerTimeout = 15
	}
	if t.PeerTimeout > 60 {
		t.PeerTimeout = 60
	}
}

func getAverage(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		sum += v
	}
	return sum / float64(len(samples))
}

func StartAITuner(ctx context.Context, aiURL string) {
	if aiURL == "" {
		aiURL = "http://localhost:8085"
	}

	log.Printf("[AI-Pilot] Initializing... waiting for system settings.")
	for i := 0; i < 30; i++ {
		if settings.BTsets != nil && settings.BTsets.TorrentDisconnectTimeout > 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	log.Printf("[AI-Pilot] Neural optimizer starting... (Stats: 5s, AI: 120s)")
	// V1.6.18: High resolution stats at 5s
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			runTuningCycle(aiURL)
		case <-ctx.Done():
			return
		}
	}
}

var lastActiveHash string

func runTuningCycle(aiURL string) {
	activeTorrents := torr.ListActiveTorrent()
	if len(activeTorrents) == 0 {
		lastKnownTotalSpeed = 0
		torrentSpeedAvg = nil
		totalSpeedAvg = nil
		cpuUsageAvg = nil
		cycleCounter = 0
		lastActiveHash = ""
		return
	}

	var activeT *torr.Torrent
	var activeStats *state.TorrentStatus
	var totalSpeedRaw float64
	realActiveCount := 0
	maxSpeed := float64(-1)

	for _, t := range activeTorrents {
		if t.Torrent == nil {
			continue
		}
		st := t.StatHighFreq()
		realActiveCount++
		totalSpeedRaw += st.DownloadSpeed
		if st.DownloadSpeed > maxSpeed {
			maxSpeed = st.DownloadSpeed
			activeT = t
			activeStats = st
		}
	}

	if activeT == nil || activeStats == nil {
		return
	}

	// V1.6.24: Reset history on Torrent Change (prevent Status 400 / Cache Mismatch)
	currentHash := activeT.Hash().String()
	if lastActiveHash != "" && currentHash != lastActiveHash {
		log.Printf("[AI-Pilot] Context Change Detected: Resetting history for new torrent.")
		metricsHistory = nil
		torrentSpeedAvg = nil
		totalSpeedAvg = nil
		cpuUsageAvg = nil
		cycleCounter = 0
		pulseCounter = 0
	}
	lastActiveHash = currentHash

	// 1. COLLECT SAMPLES (Every 5s from Ticker)
	currSpeedMBs := activeStats.DownloadSpeed / (1024 * 1024)
	totalSpeedMBs := totalSpeedRaw / (1024 * 1024)
	currentCPU := float64(getCPUUsage())

	torrentSpeedAvg = append(torrentSpeedAvg, currSpeedMBs)
	if len(torrentSpeedAvg) > 24 {
		torrentSpeedAvg = torrentSpeedAvg[1:]
	}

	totalSpeedAvg = append(totalSpeedAvg, totalSpeedMBs)
	if len(totalSpeedAvg) > 24 {
		totalSpeedAvg = totalSpeedAvg[1:]
	}

	cpuUsageAvg = append(cpuUsageAvg, currentCPU)
	if len(cpuUsageAvg) > 24 {
		cpuUsageAvg = cpuUsageAvg[1:]
	}
	lastKnownTotalSpeed = totalSpeedMBs

	// 2. AI THROTTLING: Only run inference every 24 samples (120s)
	cycleCounter++
	if cycleCounter < 24 {
		return
	}
	cycleCounter = 0

	// --- AI INFERENCE BLOCK (Every 120s) ---
	avgTorrentSpeed := getAverage(torrentSpeedAvg)
	avgCPU := getAverage(cpuUsageAvg)

	buffer := 100
	if activeT.GetCache() != nil {
		cs := activeT.GetCache().GetState()
		if cs.Capacity > 0 {
			buffer = int(cs.Filled * 100 / cs.Capacity)
		}
	}

	currentSnap := sanitizeStr(fmt.Sprintf("[CPU:%d%%, Buf:%d%%, Peers:%d, Speed:%.1fMB/s] (AVG 120s)",
		int(avgCPU), buffer, activeStats.ActivePeers, avgTorrentSpeed))
	metricsHistory = append(metricsHistory, currentSnap)
	if len(metricsHistory) > 2 {
		metricsHistory = metricsHistory[1:]
	}
	historyStr := strings.Join(metricsHistory, " -> ")

	fSize := activeT.Size
	if fSize == 0 {
		fSize = activeT.Torrent.Length()
	}
	fileSizeGB := float64(fSize) / (1024 * 1024 * 1024)

	contextStr := sanitizeStr(fmt.Sprintf("Ctx: S:%.1fGB P:%d B:%d%% V:%.1fMB/s CPU:%d%%",
		fileSizeGB, activeStats.ActivePeers, buffer, avgTorrentSpeed, int(avgCPU)))

	// V1.7.1: Professional Grade Controller (Bias towards mid-range, Temp 0.1)
	prompt := fmt.Sprintf("<|im_start|>system\nYou are a high-precision BitTorrent Tuning unit.\nContext: %s\nTrends: %s\nRULES:\n1. Consider Torrent Size of %.1fGB for connections_limit for stable 4K streaming.\n2. If CPU_Pressure is CRITICAL.\n3. Output ONLY the following JSON format: {\"connections_limit\": N, \"peer_timeout\": M}\n4. ANSWER ONLY THE JSON.\nExample: {\"connections_limit\": 35, \"peer_timeout\": 25}<|im_end|>\n<|im_start|>user\nAnalyze trends and file size. DECIDE.<|im_end|>\n<|im_start|>user\nDecide optimal values.<|im_end|>\n<|im_start|>assistant\n",
		contextStr, historyStr)

	tweak, err := fetchAIJSON[AITweak](aiURL, prompt)
	if err != nil {
		log.Printf("[AI-Pilot] Communication Delay: %v", err)
		return
	}

	tweak.Sanitize()

	if activeT.Torrent != nil {
		oldConns := activeT.Torrent.MaxEstablishedConns()
		oldTimeout := lastTimeout

		newConns := int(tweak.ConnectionsLimit)
		newTimeout := int(tweak.PeerTimeout)

		// Hysteresis: Skip if no change
		if newConns == lastConns && newTimeout == lastTimeout {
			pulseCounter++
			if pulseCounter >= 5 { // Every ~10 minutes (5 * 120s)
				log.Printf("[AI-Pilot] Pulse: Optimizer active, values stable at Conns(%d) Timeout(%ds). Metrics: %s",
					lastConns, lastTimeout, currentSnap)
				pulseCounter = 0
			}
			return
		}
		pulseCounter = 0 // Reset on actual change

		activeT.Torrent.SetMaxEstablishedConns(newConns)
		atomic.StoreInt32(&CurrentLimit, int32(newConns))
		activeT.AddExpiredTime(time.Duration(newTimeout) * time.Second)
		lastConns = newConns
		lastTimeout = newTimeout

		log.Printf("[AI-Pilot] Optimizer applying change: Conns(%d->%d) Timeout(%ds->%ds) [Metrics: %s] [Ctx: %s]",
			oldConns, lastConns, oldTimeout, lastTimeout, currentSnap, contextStr)
	}

}

func fetchAIJSON[T any](url string, prompt string) (*T, error) {
	start := time.Now()
	reqBody, _ := json.Marshal(map[string]interface{}{
		"prompt": prompt, "n_predict": 48, "temperature": 0.1,
		"stop":         []string{"<|im_end|>"},
		"cache_prompt": false,
	})
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Post(url+"/completion", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Status %d | Body: %s", resp.StatusCode, string(body))
	}

	var aiResp struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		return nil, fmt.Errorf("AI decode error: %v", err)
	}

	trimmed := strings.TrimSpace(aiResp.Content)
	if trimmed == "" {
		return nil, fmt.Errorf("empty AI response")
	}

	// V1.7.3: Minimalist auto-repair for truncated JSON
	if !strings.HasSuffix(trimmed, "}") && strings.HasPrefix(trimmed, "{") {
		trimmed += "}"
	}

	log.Printf("[AI-Pilot] RAW: %q | Latency: %v", trimmed, time.Since(start))

	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("invalid JSON structure")
	}

	var result T
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return nil, fmt.Errorf("JSON unmarshal error: %v", err)
	}
	return &result, nil
}

func getCPUUsage() int {
	t1Total, t1Idle := readCPUSample()
	time.Sleep(500 * time.Millisecond)
	t2Total, t2Idle := readCPUSample()
	totalDiff := t2Total - t1Total
	idleDiff := t2Idle - t1Idle
	if totalDiff == 0 {
		return 0
	}
	return int(100 * (totalDiff - idleDiff) / totalDiff)
}

func readCPUSample() (uint64, uint64) {
	data, _ := os.ReadFile("/proc/stat")
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 {
		return 0, 0
	}
	var total uint64
	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseUint(fields[i], 10, 64)
		total += val
	}
	idle, _ := strconv.ParseUint(fields[4], 10, 64)
	return total, idle
}

func sanitizeStr(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 31 && r < 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
