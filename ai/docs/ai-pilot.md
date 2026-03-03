# AI Pilot for GoStream (Unified Engine)

**AI Pilot** is an embedded neural optimization engine for the Raspberry Pi 4. It uses a quantized LLM (Qwen2.5-0.5B via llama.cpp) to autonomously tune BitTorrent parameters in real-time, balancing download speed with CPU thermal limits.

## Architecture

The system consists of two main components running on the Raspberry Pi:

1.  **AI Server (`ai-server.service`)**:
    *   **Engine**: `llama.cpp` server mode.
    *   **Model**: `qwen2.5-0.5b-instruct-q4_k_m.gguf` (Quantized for ARM).
    *   **Port**: `8085` (Internal).
    *   **RAM Usage**: ~550MB (Actual) / 800MB (Hard limit set in service).
    *   **Resources**: Limited to 2 threads, 256 context size.
    *   **Priority**: `Nice=15` (Low priority) to never interrupt video playback.

2.  **AI Tuner (`gostream/ai`)**:
    *   **Routine**: Runs inside `gostream` binary (Goroutine).
    *   **Interval**: Every 60 seconds.
    *   **Action**: Reads system CPU, Buffer health, and Peer stats -> Sends prompt to AI -> Applies JSON tweaks directly to GoStorm RAM.

---

## Logic & Decision Table

The AI operates in **"Machine Mode"** (V1.4.15) with a rigid decision table to prevent hallucinations. It targets a 4K Fiber Optic scenario.

| Scenario | Condition | ConnectionsLimit | PeerTimeout | Goal |
| :--- | :--- | :--- | :--- | :--- |
| **EMERGENCY** | CPU > 85% | **15** | **15s** | Cool down CPU immediately to prevent stuttering. |
| **RECOVERY** | Buffer < 40% | **35-50** | **25s** | Aggressively find new peers to refill buffer. |
| **STABLE** | Normal | **25** | **60s** | Maintain cruise speed with minimal overhead. |
| **OPTIMAL** | Buffer > 90% & CPU < 50% | **15** | **60s** | "Eco Mode": Use few high-quality peers (Fiber). |

*Note: The AI can choose intermediate values (step 5) based on the trend history.*

---

## Log Interpretation

Logs are printed to standard output (captured by systemd/journalctl).

```text
[AI-Tuner] RAM_UPDATE: Conns(30->15) Timeout(30s->60s) [Metrics: [CPU:40%, Buf:101%, Peers:15, Speed:9.0MB/s]]
```

*   **Conns(30->15)**: The torrent engine limit was lowered from 30 to 15 peers.
*   **Timeout(30s->60s)**: The peer disconnect timeout was relaxed to 60s.
*   **[Metrics]**: Snapshot of the system state that triggered this decision.

---

## Management

### Managing the AI Service
```bash
# Check status
sudo systemctl status ai-server

# Restart (if stuck)
sudo systemctl restart ai-server

# View AI specific logs
journalctl -u ai-server -f
```

### Disabling the AI
To disable the AI Pilot, set the environment variable in `config.json` or `.env` to an empty string, or stop the service:
```bash
sudo systemctl stop ai-server
sudo systemctl disable ai-server
```

---

## Tuning for Other Hardware

If migrating to a more powerful device (e.g., Pi 5, N100), update `GoStream/ai/ai-server.service`:
*   Increase threads: `-t 4`
*   Increase context: `-c 2048`
*   Switch model to **Llama-3.2-1B** for better reasoning.

## Source Code
*   **Tuner Logic**: `GoStream/ai/ai_tuner.go`
*   **Service Def**: `GoStream/ai/ai-server.service`
*   **Integration**: `GoStream/main.go` (Line ~2997)
