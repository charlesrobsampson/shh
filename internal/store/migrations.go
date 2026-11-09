package store

// migrations are applied in order, each in its own transaction. Never edit one that has shipped; append instead.
var migrations = []string{
	`
CREATE TABLE focus_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL DEFAULT 'escalation' CHECK (kind IN ('baseline', 'escalation')),
	parent_id INTEGER REFERENCES focus_sessions(id),
	start_time DATETIME NOT NULL,
	end_time DATETIME,
	target_duration_minutes INTEGER,
	focus_percentage INTEGER NOT NULL CHECK (focus_percentage BETWEEN 0 AND 99),
	then_level INTEGER CHECK (then_level IS NULL OR then_level BETWEEN 0 AND 99),
	paused_at DATETIME,
	remaining_secs INTEGER,
	note TEXT,
	total_blocked INTEGER NOT NULL DEFAULT 0,
	total_allowed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT NOT NULL,
	sender TEXT NOT NULL,
	title TEXT,
	body TEXT NOT NULL,
	timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	status TEXT NOT NULL CHECK (status IN ('blocked', 'alerted')),
	predicted_score REAL NOT NULL CHECK (predicted_score BETWEEN 0 AND 1),
	time_sensitivity_boost REAL NOT NULL DEFAULT 0.0 CHECK (time_sensitivity_boost BETWEEN 0 AND 0.4),
	is_dismissed_inbox BOOLEAN NOT NULL DEFAULT 0,
	is_trained BOOLEAN NOT NULL DEFAULT 0,
	user_feedback TEXT CHECK (user_feedback IS NULL OR user_feedback IN ('y', 'n')),
	bundle_id TEXT,
	source_rec_id INTEGER,
	threshold_at_decision INTEGER CHECK (threshold_at_decision IS NULL OR threshold_at_decision BETWEEN 0 AND 99),
	user_label INTEGER CHECK (user_label IS NULL OR user_label BETWEEN 0 AND 100),
	reason TEXT,
	is_read BOOLEAN NOT NULL DEFAULT 0,
	session_id INTEGER REFERENCES focus_sessions(id),
	summary TEXT
);
CREATE INDEX notifications_timestamp ON notifications (timestamp);
CREATE INDEX notifications_inbox ON notifications (is_dismissed_inbox, timestamp);
CREATE INDEX notifications_train ON notifications (is_trained, timestamp);
CREATE UNIQUE INDEX notifications_source_rec_id ON notifications (source_rec_id);

CREATE TABLE model_weights (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	feature_version TEXT NOT NULL,
	weights_blob BLOB NOT NULL,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX model_weights_version ON model_weights (feature_version, id);

CREATE TABLE calendar_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	start_time DATETIME NOT NULL,
	alert_at DATETIME NOT NULL,
	fired BOOLEAN NOT NULL DEFAULT 0,
	source_notification_id INTEGER REFERENCES notifications(id) ON DELETE SET NULL,
	UNIQUE (title, start_time)
);

CREATE TABLE unparsed_calendar (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	notification_id INTEGER REFERENCES notifications(id) ON DELETE SET NULL,
	category TEXT,
	title TEXT,
	subtitle TEXT,
	raw_text TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE training_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	notification_id INTEGER REFERENCES notifications(id) ON DELETE SET NULL,
	label TEXT NOT NULL,
	predicted REAL,
	delta_blob BLOB,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`,

	// macOS reuses record ids once rows are dismissed (spike 02), so a record id alone cannot identify a notification.
	`
DROP INDEX notifications_source_rec_id;
CREATE UNIQUE INDEX notifications_source_rec_id ON notifications (source_rec_id, timestamp);
`,

	// A running timed level needs its expiry stored; a paused one keeps remaining_secs instead. Only one baseline may be active.
	`
ALTER TABLE focus_sessions ADD COLUMN expires_at DATETIME;
CREATE UNIQUE INDEX focus_sessions_one_active_baseline ON focus_sessions (kind) WHERE kind = 'baseline' AND end_time IS NULL;
CREATE INDEX focus_sessions_active ON focus_sessions (end_time);
`,

	// The tailer's progress: a time cursor (macOS record ids are reused and written late, so they cannot be the cursor) and the
	// (record id, delivery time) pairs already handled inside the lookback window.
	`
CREATE TABLE ingest_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	cursor DATETIME NOT NULL
);
CREATE TABLE ingest_seen (
	rec_id INTEGER NOT NULL,
	delivered DATETIME NOT NULL,
	PRIMARY KEY (rec_id, delivered)
);
CREATE INDEX ingest_seen_delivered ON ingest_seen (delivered);
`,

	// The kind of conversation (direct, group, meeting, channel) when the app's own metadata says, so that features can be worked out
	// again from a stored row exactly as they were when it was decided.
	`
ALTER TABLE notifications ADD COLUMN thread TEXT;
`,

	// The subtitle and category as the app sent them, next to the title as the app sent it (it used to be stored with the subtitle
	// joined on), so that a notification's features can be worked out again from its row exactly as they were when it was decided.
	`
ALTER TABLE notifications ADD COLUMN subtitle TEXT;
ALTER TABLE notifications ADD COLUMN category TEXT;
`,

	// A notification the user marked, from its alert, as one to look at again in the Train tab (task 50).
	`
ALTER TABLE notifications ADD COLUMN is_flagged BOOLEAN NOT NULL DEFAULT 0;
`,

	// Routines (task 52): saved sequences of focus sessions. A run records which routine, where it has got to and which focus level
	// its current step is, so a restarted worker carries on; each step's level is kept so the summary can total what the run did. Only
	// one run can be active at a time.
	`
CREATE TABLE routine_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	routine TEXT NOT NULL,
	started_at DATETIME NOT NULL,
	ended_at DATETIME,
	end_reason TEXT,
	position INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'waiting')),
	steps_total INTEGER NOT NULL,
	level_id INTEGER REFERENCES focus_sessions(id),
	blocked INTEGER NOT NULL DEFAULT 0,
	allowed INTEGER NOT NULL DEFAULT 0,
	active_secs INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX routine_runs_one_active ON routine_runs ((1)) WHERE ended_at IS NULL;
CREATE TABLE routine_run_steps (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES routine_runs(id),
	position INTEGER NOT NULL,
	name TEXT NOT NULL,
	level_id INTEGER REFERENCES focus_sessions(id),
	started_at DATETIME NOT NULL
);
CREATE INDEX routine_run_steps_run ON routine_run_steps (run_id);
`,

	// An alert for when a timed focus session runs out (task 52): none, sound or banner. alert_done says it has been given, so a restart
	// does not give it again.
	`
ALTER TABLE focus_sessions ADD COLUMN alert TEXT;
ALTER TABLE focus_sessions ADD COLUMN alert_done BOOLEAN NOT NULL DEFAULT 0;
`,
}
