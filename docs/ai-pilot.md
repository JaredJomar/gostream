# AI GoStream Pilot Overview [Experimental]

> ### Author's Note – Why This Is Interesting
>
> This project explores a non-traditional application of AI: using a locally quantized Large Language Model (LLM) as a real-time policy engine to dynamically optimize BitTorrent runtime parameters during media streaming.
>
> Traditional torrent clients rely on static configuration or deterministic heuristics to manage peer connections, timeouts, and bandwidth behavior. In contrast, GoStream's AI Pilot periodically analyzes live system metrics (CPU load, buffer state, peer count, throughput, contextual usage scenario) and adapts operational parameters in real time.
>
> What makes this approach interesting is not the use of AI for content generation, but the use of a lightweight LLM as a decision layer inside a constrained edge environment (e.g., Raspberry Pi), controlling a distributed P2P network workload under streaming conditions (including 4K media). The model operates with a reduced context window and low resource footprint, demonstrating that adaptive AI-driven control loops can function on minimal hardware without cloud dependency.
>
> This experiment reframes an LLM from a conversational tool into a runtime optimization component effectively acting as a soft, contextual control system layered on top of a torrent engine.
>
> While experimental, this approach opens discussion around AI-assisted network tuning, adaptive peer management, and lightweight autonomous optimization in decentralized systems.
>
> Author: **Matteo Rancilio**
>
> .



**AI GoStream Pilot** is an **optional** autonomous optimization engine designed for GoStream on Raspberry Pi 4. It leverages a tiny local LLM (**Qwen3-0.6B-Q4_K_M**) to dynamically tune BitTorrent parameters, achieving two critical goals:

## Optional Activation
The system is designed to be plug-and-play and entirely decoupled:
*   **Auto-Detection**: GoStream automatically attempts to connect to the AI Server on port `8085`.
*   **Auto-Disable**: If the server is unreachable (`connection refused`), the AI Pilot disables itself after the first failed attempt and logs a single message. Subsequent cycles are silent. Re-enable by restarting GoStream.
*   **Silent Fallback**: If the server returns errors (non-connection issues), GoStream logs a `Communication Delay` and maintains current settings without affecting playback.
*   **Zero Impact**: The streaming pipeline does not wait for AI responses; if there's a communication delay, the current settings are maintained without affecting playback.
1.  **4K Stabilization**: It protects the system from CPU spikes and thermal stress by scaling down resources when performance is optimal.
2.  **Discovery Boost**: It actively attempts to improve connectivity for "difficult" or low-peer torrents by experimenting with higher connection limits and aggressive timeouts to discover faster seeders.


## Core Architecture

1.  **AI Server**: A background service (`ai-server.service`) running `llama.cpp`. It hosts the quantized model and provides a local API on port `8085`. Configured with a context window of **512 tokens** and `Nice=15` (low CPU priority).
2.  **AI Tuner**: A background goroutine within GoStream that samples system metrics every **5 seconds** and invokes the AI for decision-making on an adaptive schedule:
    - **Normal mode**: every 300 seconds (60 samples × 5s)
    - **Crisis mode**: every 60 seconds (12 samples × 5s) — activated when 5-minute average speed drops below **3.0 MB/s** (~24 Mbps, the minimum bitrate for a typical 4K movie at 20GB/2h)

## Model

| Parameter | Value |
|-----------|-------|
| Model | `Qwen_Qwen3-0.6B-Q4_K_M.gguf` |
| Context window | 512 tokens (`-c 512`) |
| Threads | 2 (`-t 2`) |
| Inference latency (cold) | ~13s on Pi 4 Cortex-A72 (idle system) |
| Inference latency (warm) | **~1.6–5s** (KV cache active) |
| Inference latency (under load) | 30–45s (CPU >90%, Pi 4 throttled) |
| RAM usage | ~545 MB |
| Prompt template | ChatML (`<\|im_start\|>`) |

The model is prompted without current-value anchors (no explicit `conns=N` in the user message) to prevent statistical echo of input values. Grammar-constrained output via GBNF forces valid JSON with 2-digit numbers. `peer_timeout_seconds` is used as the JSON key to provide semantic unit context to the model without explicit range anchoring.

### Model Selection Notes

Qwen3-0.6B was chosen over Llama-3.2-1B-Instruct-Q3_K_M after field testing:

| | Qwen3-0.6B Q4_K_M | Llama-3.2-1B Q3_K_M |
|---|---|---|
| File on disk | 462 MB | 659 MB |
| RAM in use | 545 MB | 735 MB |
| Latency (cold) | ~13s | ~15–20s |
| Latency (warm) | **~1.6s** | 9–20s |
| Token/s | ~9 tok/s | ~1–2 tok/s |
| Strategy | Proactive (preemptive peer rebuild) | Reactive (conservative) |
| Thinking mode | Blocked by grammar | N/A |

Both models produced correct decisions confirmed by bandwidth graphs. Qwen3-0.6B uses significantly less RAM and reaches ~1.6s warm latency vs 9–20s for Llama.

## Operational Logic

The AI acts as a "Pilot" observing trends through a moving average window:
*   **Context Change Detection**: Automatically detects when a new torrent is played (via InfoHash) and resets all history, averages, and baseline values to ensure decisions are based only on the current film.
*   **History Management**: Maintains **4 snapshots** of previous metrics (20-minute window) to provide temporal context.
*   **Baseline from Config**: `connections_limit` and `peer_timeout` baselines are read from `config.json` at startup, not hardcoded. Context change resets `lastConns`/`lastTimeout` to these defaults (not to zero).
*   **Surgical Sanitization**: All prompt data is stripped of non-ASCII characters to prevent backend errors.
*   **KV Cache**: Uses `cache_prompt: true` — the static system message prefix is cached between cycles, reducing latency on successive requests.
*   **No Anchoring**: Current parameter values are intentionally excluded from the prompt. Providing current values as context causes small models to statistically echo them (anchor effect). The model reasons from metrics alone.
*   **Player State Signal**: Distinguishes `state=streaming` (download active), `state=consuming` (player consuming buffer with no download), and `state=paused` (no download, stable buffer), based on buffer delta between cycles.
*   **Multi-Stream Safety**: If more than one torrent is active simultaneously:
    - If exactly **one torrent has `IsPriority=true`** (active stream), the AI tunes that torrent only — other torrents (e.g., Plex background scans) are ignored.
    - If zero or multiple priority torrents exist, all torrents are reset to config defaults.

## Prompt Design

The prompt includes swarm size, optional history, and player state signal:

```
system: Tune BitTorrent parms for performace 4K Movie streaming.
        connections_limit MUST be between 10-60.
        peer_timeout_seconds MUST be between 10-60.
        Output JSON: {"connections_limit":N,"peer_timeout_seconds":M}

user:   actual Peers in Swarm 47 - history=[CPU:60% (Peak:85%), Buf:99%, Peers:22, Speed:21MB/s (STABLE)] -> [CPU:54% (Peak:87%), Buf:100%, Peers:20, Speed:17MB/s (DOWN)] speed=10MB/s cpu=54% buf=100% peers=20 trend=DOWN (-7MB/s) state=streaming
```

Key design decisions:
- **`actual Peers in Swarm N`** prefix — provides total swarm size as context for connection headroom reasoning, without anchoring `connections_limit` to that value
- **History prefix** — up to 4 snapshots of previous cycles (every 5 minutes) give the model trend context: "speed declining for 3 cycles" vs "just dropped"
- **`state=`** signal — allows the model to distinguish `consuming` (urgent: download not keeping up) from `paused` (not urgent: player stopped)
- **Both MUST constraints** — required for the model to differentiate `peer_timeout_seconds` across scenarios; without them, timeout was always 10s
- **No seeders field** — tested and removed: the model pattern-matched seeder count into timeout value (e.g., `seeders=10` → `timeout=10`), adding noise rather than signal
- **Grammar** `number ::= [1-9] [0-9]?` → limits output to 1–2 digit numbers (1–99)
- **`Sanitize()`** clamps final values to `[10–60]` as safety net

## Inference Quality Assessment

Based on field testing (Qwen3-0.6B, March 2026): **6–7/10**

**Strengths:**
- Correctly adapts `peer_timeout` between cycles: crisis → 10s (aggressive churn), recovery → 60s (stabilize working peers). This is non-trivial contextual reasoning.
- Crisis + consuming state → raises `connections_limit` to seek more peers. Directionally correct.
- Does not produce absurd outputs (e.g., conns=60 when CPU=90%)

**Weaknesses:**
- Tends to mirror `active_peers` count in `connections_limit` (e.g., peers=13 → conns=13) rather than reasoning about swarm headroom
- `peer_timeout` varies between identical scenarios across separate runs — some instability
- High inference latency under Pi load (30–45s) limits crisis-mode effectiveness

The primary contribution of the AI layer is **adaptive `peer_timeout`**: extending it when speed is recovering to stabilize working connections, shortening it aggressively during peer collapse. `connections_limit` adjustments are more mechanical.

## Real-Time Adjustments

*   **Connections Limit**: Scaled between **10 and 60** peers. The AI prioritizes stability when CPU is high or streaming is smooth, and explores higher limits when peers are scarce or speed is declining.
*   **Peer Timeout**: Higher values reduce peer churn (keep good peers longer); lower values cycle through bad peers faster on struggling torrents.
*   **Hysteresis & Pulse**: Changes are only applied and logged when parameters actually change. A **Pulse log** is emitted every 5 stable cycles to confirm the optimizer is active.
*   **Multi-Stream Safety**: See Operational Logic above.

## Installation & Setup

1.  **Deploy AI Directory**:
    ```bash
    rsync -avz GoStream/ai/ pi@192.168.1.2:/home/pi/GoStream/ai/
    ```

2.  **Run Setup Script**:
    ```bash
    ssh pi@192.168.1.2 "cd /home/pi/GoStream/ai && chmod +x setup_pi.sh && ./setup_pi.sh"
    ```

3.  **Service Management** (start manually, does not auto-start on boot):
    ```bash
    sudo systemctl start ai-server
    # To enable auto-start:
    sudo systemctl enable ai-server
    ```

## Fail-Safe Design

If the AI Server is unreachable or returns malformed data, GoStream automatically maintains the last known good settings. Grammar-constrained generation (GBNF) ensures the model can only produce syntactically valid JSON. The `Sanitize()` method clamps values to safe ranges `[10–60]` before applying any change.

## Key Files
*   Logic: `GoStream/ai/ai_tuner.go`
*   Service: `GoStream/ai/ai-server.service`
*   Model: `GoStream/ai/models/Qwen_Qwen3-0.6B-Q4_K_M.gguf`
*   Metrics: `:8096/metrics` (includes `ai_current_limit`)

## Real-World Activity Logs

Below are examples of how the AI Pilot behaves during a typical streaming session (Pi 4, March 2026):

### 1. Startup
```text
2026/03/09 22:22:22 [AI-Pilot] Neural optimizer starting... (Stats: 5s, AI: 300s) baseline conns=25 timeout=15
```

### 2. New Torrent Detection (History Reset)
```text
2026/03/09 22:22:25 [AI-Pilot] Context Change Detected: Resetting history for new torrent.
```

### 3. Dynamic Optimization — Normal conditions (Qwen3-0.6B, March 2026)
```text
// Speed declining, CPU high → reduce connections (cold request, 12.7s)
2026/03/09 22:27:55 [AI-Pilot] Optimizer applying change: Conns(25->15) Timeout(15s->15s) [Metrics: [CPU:54% (Peak:85%), Buf:98%, Peers:22, Speed:9.6MB/s (DOWN (-2.2MB/s))]]

// Speed still declining but slower, buffer full → hold connections, nudge timeout up (5.8s)
2026/03/09 22:32:53 [AI-Pilot] Optimizer applying change: Conns(15->15) Timeout(15s->18s) [Metrics: [CPU:51% (Peak:87%), Buf:99%, Peers:15, Speed:8.3MB/s (DOWN (-1.3MB/s))]]

// Player paused (speed=0), peer pool collapsed to 2 → preemptive rebuild + max timeout (4.4s)
2026/03/09 22:37:52 [AI-Pilot] Optimizer applying change: Conns(15->20) Timeout(18s->60s) [Metrics: [CPU:23% (Peak:62%), Buf:93%, Peers:2, Speed:0.0MB/s (DOWN (-9.9MB/s))]]
```

### 4. Dynamic Optimization — Under load (Plex scan + 4K stream, Pi CPU >90%)
```text
// Speed crashed from 27MB/s to 3.6MB/s → reduce conns to match active peers, aggressive churn (33.7s latency — Pi under load)
2026/03/10 13:30:43 [AI-Pilot] RAW: "{\"connections_limit\":13,\"peer_timeout_seconds\":10}" | Latency: 33.720322922s
2026/03/10 13:30:43 [AI-Pilot] Optimizer applying change: Conns(25->13) Timeout(15s->10s) [Metrics: [CPU:70% (Peak:91%), Buf:99%, Peers:13, Speed:3.6MB/s (DOWN (-24.0MB/s))]]

// Speed recovered to 15.2MB/s → stabilize working peers (timeout 10s→60s), hold conns (44.6s latency)
2026/03/10 13:36:19 [AI-Pilot] RAW: "{\"connections_limit\":13,\"peer_timeout_seconds\":60}" | Latency: 44.647535885s
2026/03/10 13:36:19 [AI-Pilot] Optimizer applying change: Conns(13->13) Timeout(10s->60s) [Metrics: [CPU:67% (Peak:89%), Buf:98%, Peers:13, Speed:15.2MB/s (UP (+8.0MB/s))]]
```

### 5. Dynamic Optimization — Llama-3.2-1B (earlier session)
```text
// Stabilize peer pool without adding connections (buffer full, speed rising)
2026/03/09 21:51:05 [AI-Pilot] Optimizer applying change: Conns(25->25) Timeout(15s->48s) [Metrics: [CPU:51% (Peak:78%), Buf:100%, Peers:23, Speed:25.5MB/s (UP (+24.3MB/s))]]

// Expand peer pool aggressively (speed rising fast, buffer full)
2026/03/09 21:17:17 [AI-Pilot] Optimizer applying change: Conns(25->50) Timeout(15s->60s) [Metrics: [CPU:60% (Peak:90%), Buf:99%, Peers:23, Speed:23.4MB/s (UP (+22.9MB/s))]]

// Scale down when player stops consuming (speed=0, CPU=0)
2026/03/09 22:01:14 [AI-Pilot] Optimizer applying change: Conns(50->15) Timeout(60s->15s) [Metrics: [CPU:16% (Peak:55%), Buf:98%, Peers:18, Speed:0.0MB/s (DOWN (-13.4MB/s))]]
```

### 5b. Dynamic Optimization — Normal load (post LLM restart, cache warming)
```text
// First cycle after restart: cold cache (26.9s). Speed declining → timeout=10, conns mirrors active peers
2026/03/10 15:25:40 [AI-Pilot] RAW: "{\"connections_limit\":24,\"peer_timeout_seconds\":10}" | Latency: 26.891475246s
2026/03/10 15:25:40 [AI-Pilot] Optimizer applying change: Conns(25->24) Timeout(15s->10s) [Metrics: [CPU:62% (Peak:84%), Buf:99%, Peers:24, Speed:16.4MB/s (DOWN (-9.3MB/s))]]

// Second cycle: cache warmer (18.1s). Speed stabilized → timeout flips to 60s to keep working peers
2026/03/10 15:30:51 [AI-Pilot] RAW: "{\"connections_limit\":24,\"peer_timeout_seconds\":60}" | Latency: 18.126118678s
2026/03/10 15:30:51 [AI-Pilot] Optimizer applying change: Conns(24->24) Timeout(10s->60s) [Metrics: [CPU:65% (Peak:83%), Buf:100%, Peers:24, Speed:11.7MB/s (STABLE)]]
```

### 6. Auto-Disable (LLM not running)
```text
2026/03/09 20:37:03 [AI-Pilot] LLM not reachable (http://127.0.0.1:8085) — auto-disabled. Restart gostream to re-enable.
```

### 7. Stability Confirmation (Pulse)
```text
2026/03/04 11:18:28 [AI-Pilot] Pulse: Optimizer active, values stable at Conns(25) Timeout(48s). Metrics: [CPU:49%, Buf:102%, Peers:15, Speed:16.5MB/s]
```
