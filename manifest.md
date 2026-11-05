# SHH (EXECUTIVE ASSISTANT GO CLI) - TECHNICAL SPECIFICATION & MANIFEST

## 1. PROJECT OVERVIEW

`shh` is a local, macOS-native Go CLI executive assistant that intercepts system notifications (Teams, Outlook, Mail, etc.) during user-defined Focus Sessions. It uses a lightweight local ONNX vector classifier paired with an active learning reinforcement loop to block distractions while letting critical alerts and time-sensitive calendar events pass through.

---

## 2. ARCHITECTURE & SYSTEM DESIGN

```

                             macOS System Events

                                      │

                                      ▼

             ┌──────────────────────────────────────────────────┐

             │ SQLite Tailer: notificationcenterdb              │

             └────────────────────────┬─────────────────────────┘

                                      │ Raw Payload

                                      ▼

             ┌──────────────────────────────────────────────────┐

             │ Calendar Pre-empt Check                          │

             │ (RSVP Event <= 1 min? -> Force Immediate Alert) │

             └────────────────────────┬─────────────────────────┘

                                      │ (Standard Alert)

                                      ▼

             ┌──────────────────────────────────────────────────┐

             │ ONNX Embedding + Local Linear Classifier (Go)    │

             └────────────────────────┬─────────────────────────┘

                                      │

                         Score >= Focus Threshold?

                                ┌─────┴─────┐

                                ▼           ▼

                             [ALLOW]    [BLOCK]

                                │           │

                                │           ▼

                                │     Persist to TUI Buffer

                                │     (30-Day TTL Retention)

                                ▼

                   ┌──────────────────────────┐

                   │ macOS High-Priority      │

                   │ Chime / Native Banner    │

                   └──────────────────────────┘

```

---

## 3. KEY CORE FEATURES & REQUIREMENTS

### 3.1 Interception Engine (macOS Native)

- **Source:** Reads directly from macOS SQLite notification database (`~/Library/Group Containers/group.com.apple.usernotificationsd/db/notificationcenterdb`).

- **Universal Support:** Captures notifications across all applications (Teams, Outlook, Slack, Mail, System Alerts, etc.) without requiring vendor-specific Azure/Entra ID registration or API permissions.

- **DND Toggle:** Triggers macOS native Focus Mode (Do Not Disturb) via AppleScript/Shortcuts upon starting focus sessions.

### 3.2 Time-Sensitivity & Pre-emption

- **Calendar Overrides:** Incoming notifications matching confirmed calendar invitations automatically trigger an alert **1 minute prior** to the start time, bypassing all classification thresholds.

- **Time-Sensitivity Heuristics:** Calculates a score boost ($+0.1$ to $+0.4$) based on message metadata, recency, and explicit urgency keywords ("now", "ASAP", "urgent", "prod").

### 3.3 Focus Level & Dynamic Threshold Math

- **Threshold Scale:** Expressed as percentage thresholds ($0\% - 100\%$) or short named tiers.

- **Decision Formula:** $\text{Interrupt} = (S_{\text{effective}} \ge \frac{\text{Percentage}}{100}) \lor \text{CalendarPreempt}$

- **Defined Tiers:**

  - `0%` / `off` ($T = 0.00$): Allow all notifications.

  - `35%` / `light` ($T = 0.35$): Blocks newsletters/FYIs; allows direct questions.

  - `65%` / `medium` ($T = 0.65$): Blocks standard questions; allows urgent items.

  - `90%` / `deep` ($T = 0.90$): Emergency override mode (PagerDuty, Prod outage).

  - `100%` / `lockdown` ($T = 1.00$): Blocks everything except calendar events starting in $\le 60\text{ seconds}$.

### 3.4 Local Classification & SGD Reinforcement Learning

- **Embedding Model:** Small local Transformer (e.g., MiniLM-L6-v2) executed via `onnxruntime-go` generating 384-dimensional vector embeddings.

- **Classifier:** Linear classifier computing probability score $S \in [0.0, 1.0]$ via vector dot-product with weight matrix $W$.

- **Active Learning Loop:** Training feedback (`y`/`n`) triggers a single-step Stochastic Gradient Descent (SGD) weight matrix update:

  $$W_{\text{new}} = W_{\text{old}} + \alpha \cdot (\text{target} - \text{predicted}) \cdot \mathbf{x}$$

  Updated weights are persisted back to local SQLite.

---

## 4. COMMAND-LINE INTERFACE (CLI) API SPECIFICATION

```bash
shh [command] [flags]
```

### Commands & Aliases

- `f, focus` — Start a focus session.
- `s, stop` — End active focus session immediately.
- `ui` — Launch interactive Bubble Tea TUI interface.
- `ls` — Non-destructive listing of blocked alerts (leaves read/trained state untouched).
- `train` — Direct CLI batch model training mode.

### Flag Parameters

- `-t, --timer <duration>` — Session length (e.g., `25m`, `50m`, `1h`, or `0` for indefinite).
- `-f, --focus <percent|tier>` — Sensitivity threshold (e.g., `75%`, `90%`, `light`, `deep`).
- `-m, --note <text>` — Notes/context string attached to focus session log.

### Syntax Examples

```bash
# 25-minute Pomodoro session with 75% focus threshold
$ shh focus -t 25m -f 75% -m "Refactoring database connection pool"

# Short syntax for open-ended deep focus session
$ shh f -f 90% "Prod OOM investigation"

# Launch full Bubble Tea TUI
$ shh ui

# Inspect blocked alerts without altering database flags
$ shh ls --source teams --since 1h
```

---

## 5. TERMINAL USER INTERFACE (TUI) DESIGN

Built using **Bubble Tea** and **Lipgloss**, utilizing Vim-style modal keybindings.

### 5.1 Dual-Tab Layout System

1. **Tab 1: Active Notifications (Inbox)**
   - **Filter:** `is_dismissed_inbox = false`.
   - **Purpose:** Operational inbox to read and triage blocked alerts. Giving training feedback here also archives the message from this view.
2. **Tab 2: Train / Tune Queue (Backlog)**
   - **Filter:** `is_trained = false`.
   - **Purpose:** Training gym containing all intercepted alerts (read or unread). Providing feedback (`y`/`n`) updates model weights and removes the item from Tab 2 without deleting it from Tab 1 (unless performed via quick-train keybindings).

### 5.2 TUI Controls & Vim Keybindings

| Key | Context | Action |
| :--- | :--- | :--- |
| `1` / `2` | Global | Switch between **Notifications** (Tab 1) and **Train** (Tab 2). |
| `j` / `k` | Navigation | Navigate table rows up / down. |
| `h` / `l` | Navigation | Switch focus between panes or detail views. |
| `o` | Either Tab | Open original notification/message in native macOS app. |
| `s` | Either Tab | Request a bullet-point summary from local Ollama daemon. |
| `d` / `x` | Tab 1 | Dismiss/Archive alert from Inbox (`is_dismissed_inbox = true`). Retains item in Tab 2. |
| `y` | Either Tab | **Train Positive:** "Should have alerted me." Runs SGD update, sets `is_trained = true`. |
| `n` | Either Tab | **Train Negative:** "Rightly blocked." Runs SGD update, sets `is_trained = true`. |
| `p` / `<Space>`| Active Session | Pause / Resume current focus timer. |
| `+` / `-` | Active Session | Adjust active session duration (+5m / -5m). |
| `f` | Active Session | Adjust focus sensitivity percentage mid-session. |

---

## 6. DATA STORAGE & PERSISTENCE (SQLite Schema)

Database File: `~/.config/shh/shh.db`

```sql
-- Tracked Notifications & Model Training State
CREATE TABLE IF NOT EXISTS notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,                     -- e.g., 'Teams', 'Outlook', 'Slack'
    sender TEXT NOT NULL,                     -- Sender name or email
    title TEXT,                               -- Notification title
    body TEXT NOT NULL,                       -- Payload content
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL,                     -- 'blocked' or 'alerted'
    predicted_score REAL NOT NULL,            -- Base ONNX raw model score [0.0 - 1.0]
    time_sensitivity_boost REAL DEFAULT 0.0,  -- Heuristic boost value [0.0 - 0.4]
    is_dismissed_inbox BOOLEAN DEFAULT 0,     -- Controls Tab 1 Inbox visibility
    is_trained BOOLEAN DEFAULT 0,             -- Controls Tab 2 Train Queue visibility
    user_feedback TEXT                        -- 'y' (should alert) or 'n' (rightly blocked)
);

-- Active & Historical Focus Sessions
CREATE TABLE IF NOT EXISTS focus_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    start_time DATETIME NOT NULL,
    end_time DATETIME,
    target_duration_minutes INTEGER,          -- 0 for indefinite sessions
    focus_percentage INTEGER NOT NULL,        -- Sensitivity threshold (0-100)
    note TEXT,                                -- Session note/task goal
    total_blocked INTEGER DEFAULT 0,          -- Metrics count
    total_allowed INTEGER DEFAULT 0           -- Metrics count
);

-- Model Weights Persistence
CREATE TABLE IF NOT EXISTS model_weights (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    weights_blob BLOB NOT NULL,               -- Binary serialized weight vector W
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Automatic TTL Maintenance

A background cleanup routine runs on application launch to purge historical entries older than 30 days, keeping the SQLite database small:

```sql
DELETE FROM notifications 
WHERE timestamp < datetime('now', '-30 days') 
  AND is_trained = 1 
  AND is_dismissed_inbox = 1;
```

---

## 7. RECOMMENDED GO PACKAGE DEPENDENCIES

| Domain | Recommended Package | Purpose |
| :--- | :--- | :--- |
| **CLI Framework** | `github.com/alecthomas/kong` or `cobra` | Super-fast CLI flag parsing with short alias bindings. |
| **TUI Interface** | `github.com/charmbracelet/bubbletea` | Core Elm-architecture TUI engine with keybindings. |
| **TUI Styling** | `github.com/charmbracelet/lipgloss` | Terminal layout, borders, highlights, and status bars. |
| **Database** | `github.com/mattn/go-sqlite3` | CGo SQLite driver for read/write database transactions. |
| **ONNX Runtime** | `github.com/yalue/onnxruntime_go` | Native CGo bindings for local ONNX sentence embeddings. |
