# AI GoStream Pilot Overview [Experimental]

**AI GoStream Pilot** is an **optional** autonomous optimization engine designed for GoStream on Raspberry Pi 4. It leverages a tiny local LLM (Qwen2.5-0.5B) to dynamically tune BitTorrent parameters, achieving two critical goals:

## Optional Activation
The system is designed to be plug-and-play and entirely decoupled:
*   **Auto-Detection**: GoStream automatically attempts to connect to the AI Server on port `8085`. 
*   **Silent Fallback**: If the server is not running, unreachable, or disabled, GoStream continues to operate normally using its standard default settings. 
*   **Zero Impact**: The streaming pipeline does not wait for AI responses; if there's a communication delay, the current settings are maintained without affecting playback.
1.  **4K Stabilization**: It protects the system from CPU spikes and thermal stress by scaling down resources when performance is optimal.
2.  **Discovery Boost**: It actively attempts to improve connectivity for "difficult" or low-peer torrents by experimenting with higher connection limits and aggressive timeouts to discover faster seeders.


## Core Architecture

1.  **AI Server**: A background service (`ai-server.service`) running `llama.cpp`. It hosts the quantized model and provides a local API on port `8085`. It is configured with a strict context window of **256 tokens** to minimize RAM usage.
2.  **AI Tuner**: A background goroutine within GoStream that samples system metrics every **5 seconds** and invokes the AI for decision-making every **60 seconds**. This "High-Fidelity Sampling / Low-Frequency Inference" approach reduces CPU overhead by 90% compared to earlier versions.

## Operational Logic

The AI acts as a "Pilot" observing trends through a moving average window:
*   **Context Change Detection**: Automatically detects when a new torrent is played (via InfoHash) and resets all history and averages to ensure decisions are based only on the current film.
*   **History Management**: Maintains exactly **2 snapshots** of previous metrics ("Trends") to stay within the 256-token limit without sacrificing temporal awareness.
*   **Surgical Sanitization**: All prompt data is stripped of non-ASCII characters to prevent backend crashes (Status 400 / Error 191).
*   **KV Cache Bypass**: Uses `cache_prompt: false` to force the server to evaluate every request independently, preventing token accumulation on limited devices.

## Real-Time Adjustments

*   **Connections Limit**: Scaled between **15 and 60** peers. The AI prioritizes safety when CPU > 75% or speed is high, and explores higher limits only when peers are scarce and CPU is low.
*   **Peer Timeout**: Lower values are used to cycle through bad peers faster on slow torrents.
*   **Hysteresis & Pulse**: To keep logs clean, changes are only logged when parameters actually change. A **Pulse log** is emitted every 5 minutes during stable periods to confirm the optimizer is still active.
*   **More to come**

## Installation & Setup

1.  **Deploy AI Directory**:
    ```bash
    rsync -avz GoStream/ai/ pi@192.168.1.2:/home/pi/GoStream/ai/
    ```

2.  **Run Setup Script**:
    ```bash
    ssh pi@192.168.1.2 "cd /home/pi/GoStream/ai && chmod +x setup_pi.sh && ./setup_pi.sh"
    ```

3.  **Service Management**:
    ```bash
    sudo systemctl enable ai-server
    sudo systemctl start ai-server
    ```

## Fail-Safe Design

If the AI Server is unreachable or returns malformed data, GoStream automatically maintains the last known good settings. The system is hardened against truncated AI responses through an internal robust JSON parser that auto-closes brackets and validates keys before applying changes.

## Key Files
*   Logic: `GoStream/ai/ai_tuner.go`
*   Service: `GoStream/ai/ai-server.service`
*   Metrics: `:8096/metrics` (includes `ai_current_limit`)

## Real-World Activity Logs

Below are examples of how the AI Pilot behaves during a typical streaming session:

### 1. New Torrent Detection (History Reset)
```text
2026/03/04 11:19:56 [AI-Pilot] Context Change Detected: Resetting history for new torrent.
```

### 2. Dynamic Optimization (Scaling Up/Down)
The AI analyzes performance every 60s and adjusts parameters only when necessary:
```text
// AI boosts connections to find more peers for a slow torrent
2026/03/04 11:39:46 [AI-Pilot] Optimizer applying change: Conns(15->35) Timeout(25s->25s) [Metrics: [CPU:30%, Buf:96%, Peers:2, Speed:1.2MB/s] (AVG 60s)]

// AI scales back resources when speed is sufficient to protect CPU
2026/03/04 11:44:58 [AI-Pilot] Optimizer applying change: Conns(35->15) Timeout(25s->25s) [Metrics: [CPU:36%, Buf:101%, Peers:4, Speed:11.0MB/s] (AVG 60s)]

// AI fine-tunes timeout to cycle peers more effectively
2026/03/04 11:40:56 [AI-Pilot] Optimizer applying change: Conns(35->27) Timeout(25s->60s) [Metrics: [CPU:27%, Buf:98%, Peers:2, Speed:1.8MB/s] (AVG 60s)]
```

### 3. Stability Confirmation (Pulse)
When parameters remain optimal, the AI stays silent and emits a "Pulse" every 5 minutes:
```text
2026/03/04 11:18:28 [AI-Pilot] Pulse: Optimizer active, values stable at Conns(15) Timeout(25s). Metrics: [CPU:49%, Buf:102%, Peers:15, Speed:16.5MB/s] (AVG 60s)
```
